package syncdb

import (
	"time"

	"gorm.io/gorm"
)

// Note represents a note synced from Notion
type Note struct {
	ID         string    `gorm:"column:id;primaryKey"`
	Content    string    `gorm:"column:content;type:text"`
	CreatedAt  time.Time `gorm:"column:createdAt"`
	UpdatedAt  time.Time `gorm:"column:updatedAt"`
	UserID     string    `gorm:"column:userId;index"`
	ExternalID *string   `gorm:"column:externalId;index"` // Notion page ID
	NotionUUID *string   `gorm:"column:notionUuid;index"` // Notion post UUID (stored in ID property)
	Tags       []Tag     `gorm:"many2many:NoteTag;foreignKey:ID;joinForeignKey:noteId;References:ID;joinReferences:tagId"`
}

// TableName specifies the table name for Note
func (Note) TableName() string {
	return "Note"
}

// Tag represents a tag in the database
type Tag struct {
	ID        string    `gorm:"column:id;primaryKey"`
	Name      string    `gorm:"column:name"`
	CreatedAt time.Time `gorm:"column:createdAt"`
	UserID    string    `gorm:"column:userId;index"`
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

// SyncState tracks the last sync time per user
type SyncState struct {
	UserID       string    `gorm:"column:userId;primaryKey"`
	LastSyncedAt time.Time `gorm:"column:lastSyncedAt"`
}

// TableName specifies the table name for SyncState
func (SyncState) TableName() string {
	return "SyncState"
}

// User represents a user in the database (read-only for sync purposes)
type User struct {
	ID    string `gorm:"column:id;primaryKey"`
	Email string `gorm:"column:email"`
}

// TableName specifies the table name for User
func (User) TableName() string {
	return "User"
}

// BeforeCreate hook to generate CUID-like ID
func (n *Note) BeforeCreate(tx *gorm.DB) error {
	if n.ID == "" {
		n.ID = generateCUID()
	}
	return nil
}

// BeforeCreate hook to generate CUID-like ID for tags
func (t *Tag) BeforeCreate(tx *gorm.DB) error {
	if t.ID == "" {
		t.ID = generateCUID()
	}
	return nil
}
