package models

import "time"

// StripeEvent records a successfully applied Stripe webhook event.
type StripeEvent struct {
	ID        string    `gorm:"column:id;primaryKey"`
	CreatedAt time.Time `gorm:"column:createdAt;not null"`
}

// TableName specifies the table name for StripeEvent.
func (StripeEvent) TableName() string {
	return "StripeEvent"
}
