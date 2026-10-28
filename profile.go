package smsapibg

import (
	"context"
	"net/http"
)

// Profile is the account behind the token. Points is the balance a caller
// usually wants, to alert before sending stops with
// [ErrInsufficientCredits]. Email and PhoneNumber are the owner's personal
// data: do not log them.
type Profile struct {
	Points      float64
	Name        string
	Email       string
	Username    string
	PhoneNumber string
	PaymentType string // "prepaid" or "postpaid"
	UserType    string // "native" or "subuser"
}

// Profile reads the account profile (GET /profile), mainly for the balance.
// The token needs the profile scope.
func (c *Client) Profile(ctx context.Context) (Profile, error) {
	var body struct {
		Points      looseFloat `json:"points"`
		Name        string     `json:"name"`
		Email       string     `json:"email"`
		Username    string     `json:"username"`
		PhoneNumber string     `json:"phone_number"`
		PaymentType string     `json:"payment_type"`
		UserType    string     `json:"user_type"`
	}
	if err := c.call(ctx, http.MethodGet, "profile", nil, &body); err != nil {
		return Profile{}, err
	}
	return Profile{
		Points:      float64(body.Points),
		Name:        body.Name,
		Email:       body.Email,
		Username:    body.Username,
		PhoneNumber: body.PhoneNumber,
		PaymentType: body.PaymentType,
		UserType:    body.UserType,
	}, nil
}
