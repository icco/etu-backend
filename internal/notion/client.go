package notion

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jomei/notionapi"
)

// Post represents a journal entry from Notion.
type Post struct {
	ID         string    // Unique identifier (UUID stored in Notion)
	PageID     string    // Notion page ID
	Tags       []string  // Tags associated with the post
	Text       string    // Content of the post
	CreatedAt  time.Time // Creation timestamp
	ModifiedAt time.Time // Last modification timestamp
}

// Client wraps the Notion API client.
type Client struct {
	notionKey  string
	rootPage   string
	cachedDbID notionapi.DatabaseID
	client     *notionapi.Client
	clientOnce sync.Once
}

// NewClient creates a new Notion client from environment variables.
func NewClient() (*Client, error) {
	notionKey := os.Getenv("NOTION_KEY")
	if notionKey == "" {
		return nil, fmt.Errorf("NOTION_KEY environment variable is required")
	}

	return NewClientWithKey(notionKey), nil
}

// NewClientWithKey creates a new Notion client with a specific API key.
// If databaseName is empty, defaults to "Journal".
func NewClientWithKey(notionKey string, databaseName ...string) *Client {
	dbName := "Journal" // Default value
	if len(databaseName) > 0 && databaseName[0] != "" {
		dbName = databaseName[0]
	}
	return &Client{
		notionKey: notionKey,
		rootPage:  dbName,
	}
}

// getClient returns a cached Notion client.
func (c *Client) getClient() *notionapi.Client {
	c.clientOnce.Do(func() {
		c.client = notionapi.NewClient(
			notionapi.Token(c.notionKey),
			notionapi.WithVersion("2022-06-28"),
			notionapi.WithRetry(2),
		)
	})
	return c.client
}

// ListAllPosts retrieves all journal entries from Notion using pagination.
func (c *Client) ListAllPosts(ctx context.Context) ([]*Post, error) {
	dbID, err := c.getDatabaseID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get database ID: %w", err)
	}

	client := c.getClient()
	var allPosts []*Post
	var cursor notionapi.Cursor

	for {
		req := &notionapi.DatabaseQueryRequest{
			Sorts: []notionapi.SortObject{
				{Property: "Created At", Direction: notionapi.SortOrderDESC},
			},
			PageSize: 100,
		}
		if cursor != "" {
			req.StartCursor = cursor
		}

		resp, err := client.Database.Query(ctx, dbID, req)
		if err != nil {
			return nil, fmt.Errorf("failed to query database: %w", err)
		}

		posts, err := c.processPages(ctx, client, resp.Results)
		if err != nil {
			return nil, fmt.Errorf("failed to process pages: %w", err)
		}

		allPosts = append(allPosts, posts...)

		if !resp.HasMore {
			break
		}
		cursor = resp.NextCursor
	}

	return allPosts, nil
}

// ListPostsSince retrieves journal entries modified since the given time.
func (c *Client) ListPostsSince(ctx context.Context, since time.Time) ([]*Post, error) {
	dbID, err := c.getDatabaseID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get database ID: %w", err)
	}

	client := c.getClient()
	var allPosts []*Post
	var cursor notionapi.Cursor

	sinceDate := notionapi.Date(since)

	for {
		req := &notionapi.DatabaseQueryRequest{
			Sorts: []notionapi.SortObject{
				{Property: "Created At", Direction: notionapi.SortOrderDESC},
			},
			Filter: &notionapi.TimestampFilter{
				Timestamp: notionapi.TimestampLastEdited,
				LastEditedTime: &notionapi.DateFilterCondition{
					OnOrAfter: &sinceDate,
				},
			},
			PageSize: 100,
		}
		if cursor != "" {
			req.StartCursor = cursor
		}

		resp, err := client.Database.Query(ctx, dbID, req)
		if err != nil {
			return nil, fmt.Errorf("failed to query database: %w", err)
		}

		posts, err := c.processPages(ctx, client, resp.Results)
		if err != nil {
			return nil, fmt.Errorf("failed to process pages: %w", err)
		}

		allPosts = append(allPosts, posts...)

		if !resp.HasMore {
			break
		}
		cursor = resp.NextCursor
	}

	return allPosts, nil
}

// processPages processes Notion pages into Post structs with parallel content fetching.
func (c *Client) processPages(ctx context.Context, client *notionapi.Client, pages []notionapi.Page) ([]*Post, error) {
	if len(pages) == 0 {
		return []*Post{}, nil
	}

	type pageResult struct {
		post *Post
		err  error
		idx  int
	}

	results := make(chan pageResult, len(pages))

	// Process all pages in parallel
	for i, page := range pages {
		go func(idx int, p notionapi.Page) {
			// Extract tags
			rawTags := p.Properties["Tags"]
			tagData, ok := rawTags.(*notionapi.MultiSelectProperty)
			if !ok {
				results <- pageResult{err: fmt.Errorf("tags property is not a multi-select: %+v", rawTags), idx: idx}
				return
			}
			var tags []string
			for _, tag := range tagData.MultiSelect {
				tags = append(tags, tag.Name)
			}

			// Extract ID
			rawID := p.Properties["ID"]
			idData, ok := rawID.(*notionapi.TitleProperty)
			if !ok {
				results <- pageResult{err: fmt.Errorf("id property is not a title: %+v", rawID), idx: idx}
				return
			}
			if len(idData.Title) == 0 {
				results <- pageResult{err: fmt.Errorf("id property is empty"), idx: idx}
				return
			}
			id := idData.Title[0].PlainText

			// Fetch full content
			text, err := c.getPageContent(ctx, client, string(p.ID))
			if err != nil {
				results <- pageResult{err: fmt.Errorf("failed to get page content: %w", err), idx: idx}
				return
			}

			results <- pageResult{
				post: &Post{
					ID:         id,
					PageID:     p.ID.String(),
					Tags:       tags,
					Text:       text,
					CreatedAt:  p.CreatedTime,
					ModifiedAt: p.LastEditedTime,
				},
				idx: idx,
			}
		}(i, page)
	}

	// Collect results in order
	posts := make([]*Post, len(pages))
	for i := 0; i < len(pages); i++ {
		result := <-results
		if result.err != nil {
			return nil, result.err
		}
		posts[result.idx] = result.post
	}

	return posts, nil
}

// getPageContent fetches the full content of a Notion page.
func (c *Client) getPageContent(ctx context.Context, client *notionapi.Client, pageID string) (string, error) {
	var text strings.Builder
	var cursor string

	for {
		pagination := &notionapi.Pagination{PageSize: 100}
		if cursor != "" {
			pagination.StartCursor = notionapi.Cursor(cursor)
		}

		blockResp, err := client.Block.GetChildren(ctx, notionapi.BlockID(pageID), pagination)
		if err != nil {
			return "", err
		}

		for _, block := range blockResp.Results {
			switch block.GetType() {
			case notionapi.BlockTypeParagraph:
				paragraph, ok := block.(*notionapi.ParagraphBlock)
				if !ok {
					return "", fmt.Errorf("paragraph is incorrect block type: %+v", block)
				}
				text.WriteString(paragraph.GetRichTextString())
				text.WriteString("\n")
			default:
				// Skip other block types
			}
		}

		if !blockResp.HasMore {
			break
		}
		cursor = blockResp.NextCursor
	}

	return strings.TrimSpace(text.String()), nil
}

// getDatabaseID retrieves and caches the Notion database ID.
func (c *Client) getDatabaseID(ctx context.Context) (notionapi.DatabaseID, error) {
	if c.cachedDbID != "" {
		return c.cachedDbID, nil
	}

	client := c.getClient()
	resp, err := client.Search.Do(ctx, &notionapi.SearchRequest{
		Query: c.rootPage,
		Filter: notionapi.SearchFilter{
			Value:    "database",
			Property: "object",
		},
	})
	if err != nil {
		return "", err
	}

	if len(resp.Results) == 0 {
		return "", fmt.Errorf("database '%s' not found", c.rootPage)
	}

	if len(resp.Results) > 1 {
		return "", fmt.Errorf("multiple databases named '%s' found", c.rootPage)
	}

	db, ok := resp.Results[0].(*notionapi.Database)
	if !ok {
		return "", fmt.Errorf("result is not a database")
	}

	c.cachedDbID = notionapi.DatabaseID(db.ID.String())
	return c.cachedDbID, nil
}

// CreatePost creates a new page in the Notion database.
// Returns the Notion page ID and UUID on success.
func (c *Client) CreatePost(ctx context.Context, id, content string, tags []string) (pageID string, err error) {
	dbID, err := c.getDatabaseID(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get database ID: %w", err)
	}

	client := c.getClient()

	// Build multi-select options for tags
	multiSelectTags := make([]notionapi.Option, len(tags))
	for i, tag := range tags {
		multiSelectTags[i] = notionapi.Option{Name: tag}
	}

	// Create the page with properties
	createReq := &notionapi.PageCreateRequest{
		Parent: notionapi.Parent{
			Type:       notionapi.ParentTypeDatabaseID,
			DatabaseID: dbID,
		},
		Properties: notionapi.Properties{
			"ID": notionapi.TitleProperty{
				Type: notionapi.PropertyTypeTitle,
				Title: []notionapi.RichText{
					{
						Type: notionapi.ObjectTypeText,
						Text: &notionapi.Text{Content: id},
					},
				},
			},
			"Tags": notionapi.MultiSelectProperty{
				Type:        notionapi.PropertyTypeMultiSelect,
				MultiSelect: multiSelectTags,
			},
		},
		Children: c.contentToBlocks(content),
	}

	page, err := client.Page.Create(ctx, createReq)
	if err != nil {
		return "", fmt.Errorf("failed to create page: %w", err)
	}

	return page.ID.String(), nil
}

// UpdatePost updates an existing Notion page's content and tags.
func (c *Client) UpdatePost(ctx context.Context, pageID, content string, tags []string) error {
	client := c.getClient()

	// Build multi-select options for tags
	multiSelectTags := make([]notionapi.Option, len(tags))
	for i, tag := range tags {
		multiSelectTags[i] = notionapi.Option{Name: tag}
	}

	// Update page properties (tags)
	updateReq := &notionapi.PageUpdateRequest{
		Properties: notionapi.Properties{
			"Tags": notionapi.MultiSelectProperty{
				Type:        notionapi.PropertyTypeMultiSelect,
				MultiSelect: multiSelectTags,
			},
		},
	}

	_, err := client.Page.Update(ctx, notionapi.PageID(pageID), updateReq)
	if err != nil {
		return fmt.Errorf("failed to update page properties: %w", err)
	}

	// Update content: first delete existing blocks, then add new ones
	if err := c.replacePageContent(ctx, client, pageID, content); err != nil {
		return fmt.Errorf("failed to update page content: %w", err)
	}

	return nil
}

// ArchivePost archives (soft-deletes) a Notion page.
func (c *Client) ArchivePost(ctx context.Context, pageID string) error {
	client := c.getClient()

	updateReq := &notionapi.PageUpdateRequest{
		Archived: true,
	}

	_, err := client.Page.Update(ctx, notionapi.PageID(pageID), updateReq)
	if err != nil {
		return fmt.Errorf("failed to archive page: %w", err)
	}

	return nil
}

// replacePageContent deletes all existing blocks and adds new content.
func (c *Client) replacePageContent(ctx context.Context, client *notionapi.Client, pageID, content string) error {
	// First, get all existing blocks
	var cursor string
	var blockIDs []notionapi.BlockID

	for {
		pagination := &notionapi.Pagination{PageSize: 100}
		if cursor != "" {
			pagination.StartCursor = notionapi.Cursor(cursor)
		}

		blockResp, err := client.Block.GetChildren(ctx, notionapi.BlockID(pageID), pagination)
		if err != nil {
			return fmt.Errorf("failed to get existing blocks: %w", err)
		}

		for _, block := range blockResp.Results {
			blockIDs = append(blockIDs, notionapi.BlockID(block.GetID()))
		}

		if !blockResp.HasMore {
			break
		}
		cursor = blockResp.NextCursor
	}

	// Delete all existing blocks
	for _, blockID := range blockIDs {
		_, err := client.Block.Delete(ctx, blockID)
		if err != nil {
			return fmt.Errorf("failed to delete block %s: %w", blockID, err)
		}
	}

	// Add new content blocks
	newBlocks := c.contentToBlocks(content)
	if len(newBlocks) > 0 {
		_, err := client.Block.AppendChildren(ctx, notionapi.BlockID(pageID), &notionapi.AppendBlockChildrenRequest{
			Children: newBlocks,
		})
		if err != nil {
			return fmt.Errorf("failed to append new blocks: %w", err)
		}
	}

	return nil
}

// contentToBlocks converts text content to Notion paragraph blocks.
func (c *Client) contentToBlocks(content string) []notionapi.Block {
	if content == "" {
		return nil
	}

	// Split content by newlines and create paragraph blocks
	lines := strings.Split(content, "\n")
	blocks := make([]notionapi.Block, 0, len(lines))

	for _, line := range lines {
		blocks = append(blocks, &notionapi.ParagraphBlock{
			BasicBlock: notionapi.BasicBlock{
				Type:   notionapi.BlockTypeParagraph,
				Object: notionapi.ObjectTypeBlock,
			},
			Paragraph: notionapi.Paragraph{
				RichText: []notionapi.RichText{
					{
						Type: notionapi.ObjectTypeText,
						Text: &notionapi.Text{Content: line},
					},
				},
			},
		})
	}

	return blocks
}
