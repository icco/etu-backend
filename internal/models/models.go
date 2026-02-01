package models

import (
	"time"

	"gorm.io/gorm"
)

// Note represents a note in the database
type Note struct {
	ID                 string      `gorm:"column:id;primaryKey"`
	Content            string      `gorm:"column:content;type:text"`
	CreatedAt          time.Time   `gorm:"column:createdAt"`
	UpdatedAt          time.Time   `gorm:"column:updatedAt"`
	UserID             string      `gorm:"column:userId;index"`
	ExternalID         *string     `gorm:"column:externalId;index"`   // Notion page ID
	NotionUUID         *string     `gorm:"column:notionUuid;index"`   // Notion post UUID (stored in ID property)
	LastSyncedToNotion *time.Time  `gorm:"column:lastSyncedToNotion"` // When this note was last pushed to Notion
	Tags               []Tag       `gorm:"many2many:NoteTag;foreignKey:ID;joinForeignKey:noteId;References:ID;joinReferences:tagId"`
	Images             []NoteImage `gorm:"foreignKey:NoteID"`
}

// TableName specifies the table name for Note
func (Note) TableName() string {
	return "Note"
}

// NoteImage represents an image attached to a note
type NoteImage struct {
	ID            string    `gorm:"column:id;primaryKey"`
	NoteID        string    `gorm:"column:noteId;index;not null"`
	URL           string    `gorm:"column:url;not null"`
	GCSObjectName string    `gorm:"column:gcsObjectName;not null"` // Object name in GCS for deletion
	ExtractedText string    `gorm:"column:extractedText;type:text"`
	MimeType      string    `gorm:"column:mimeType"`
	CreatedAt     time.Time `gorm:"column:createdAt"`
}

// TableName specifies the table name for NoteImage
func (NoteImage) TableName() string {
	return "NoteImage"
}

// Tag represents a tag in the database
type Tag struct {
	ID        string    `gorm:"column:id;primaryKey"`
	Name      string    `gorm:"column:name"`
	CreatedAt time.Time `gorm:"column:createdAt"`
	UserID    string    `gorm:"column:userId;index"`
	Count     int       `gorm:"-"` // Computed field, not stored in DB
}

// TableName specifies the table name for Tag
func (Tag) TableName() string {
	return "Tag"
}

// NoteTag represents the many-to-many relationship between Note and Tag
type NoteTag struct {
	NoteID string `gorm:"column:noteId;primaryKey"`
	TagID  string `gorm:"column:tagId;primaryKey"`
}

// TableName specifies the table name for NoteTag
func (NoteTag) TableName() string {
	return "NoteTag"
}

// User represents a user in the database
type User struct {
	ID                 string     `gorm:"column:id;primaryKey"`
	Email              string     `gorm:"column:email"`
	Name               *string    `gorm:"column:name"`
	Image              *string    `gorm:"column:image"`
	PasswordHash       string     `gorm:"column:passwordHash"`
	SubscriptionStatus string     `gorm:"column:subscriptionStatus"`
	SubscriptionEnd    *time.Time `gorm:"column:subscriptionEnd"`
	CreatedAt          time.Time  `gorm:"column:createdAt"`
	StripeCustomerID   *string    `gorm:"column:stripeCustomerId"`
	NotionKey          *string    `gorm:"column:notionKey"` // Notion API key for syncing. TODO: Consider encrypting this field at rest for better security.
	UpdatedAt          time.Time  `gorm:"column:updatedAt"`
}

// TableName specifies the table name for User
func (User) TableName() string {
	return "User"
}

// ApiKey represents an API key in the database
type ApiKey struct {
	ID        string     `gorm:"column:id;primaryKey"`
	Name      string     `gorm:"column:name"`
	KeyPrefix string     `gorm:"column:keyPrefix"`
	KeyHash   string     `gorm:"column:keyHash"`
	UserID    string     `gorm:"column:userId;index"`
	CreatedAt time.Time  `gorm:"column:createdAt"`
	LastUsed  *time.Time `gorm:"column:lastUsed"`
}

// TableName specifies the table name for ApiKey
func (ApiKey) TableName() string {
	return "ApiKey"
}

// SyncState tracks the last sync time per user
type SyncState struct {
	UserID       string    `gorm:"column:userId;primaryKey"`
	LastSyncedAt time.Time `gorm:"column:lastSyncedAt"`
}

// TableName specifies the table name for SyncState
func (SyncState) TableName() string {
	return "SyncState"
}

// BeforeCreate hook to generate CUID-like ID for notes
func (n *Note) BeforeCreate(tx *gorm.DB) error {
	if n.ID == "" {
		n.ID = GenerateCUID()
	}
	return nil
}

// BeforeCreate hook to generate CUID-like ID for tags
func (t *Tag) BeforeCreate(tx *gorm.DB) error {
	if t.ID == "" {
		t.ID = GenerateCUID()
	}
	return nil
}

// BeforeCreate hook to generate CUID-like ID for users
func (u *User) BeforeCreate(tx *gorm.DB) error {
	if u.ID == "" {
		u.ID = GenerateCUID()
	}
	return nil
}

// BeforeCreate hook to generate CUID-like ID for API keys
func (a *ApiKey) BeforeCreate(tx *gorm.DB) error {
	if a.ID == "" {
		a.ID = GenerateCUID()
	}
	return nil
}

// BeforeCreate hook to generate CUID-like ID for note images
func (ni *NoteImage) BeforeCreate(tx *gorm.DB) error {
	if ni.ID == "" {
		ni.ID = GenerateCUID()
	}
	return nil
}

// GenerateCUID generates a CUID-like identifier
func GenerateCUID() string {
	const chars = "0123456789abcdefghijklmnopqrstuvwxyz"
	result := make([]byte, 25)
	result[0] = 'c'

	timestamp := time.Now().UnixMilli()
	for i := 1; i < 9; i++ {
		result[i] = chars[timestamp%36]
		timestamp /= 36
	}

	for i := 9; i < 25; i++ {
		result[i] = chars[time.Now().UnixNano()%36]
	}

	return string(result)
}
