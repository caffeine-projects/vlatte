// Package payments is a client of a payments API: the code that the HTTP example of the docs home page tests against a
// mock server.
package payments

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"
)

// Client charges payments through the payments API at its base URL.
type Client struct {
	baseURL string
	token   string
}

// NewClient creates a Client of the API at baseURL, such as a mock server's URL, that authenticates with the bearer
// token.
func NewClient(baseURL, token string) *Client {
	return &Client{baseURL: baseURL, token: token}
}

// Charge charges amount and returns the ID of the payment the API created. It fails when the API answers with a
// status other than 201 Created.
func (c *Client) Charge(ctx context.Context, amount int) (string, error) {
	body, err := json.Marshal(chargeRequest{Amount: amount})
	if err != nil {
		return "", fmt.Errorf("payments: encoding the charge: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/payments", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("payments: creating the request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("payments: charging: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("payments: expected status 201, not %d", res.StatusCode)
	}

	var payment chargeResponse
	if err := json.UnmarshalRead(res.Body, &payment); err != nil {
		return "", fmt.Errorf("payments: reading the payment: %w", err)
	}

	return payment.ID, nil
}

type chargeRequest struct {
	Amount int `json:"amount"`
}

type chargeResponse struct {
	ID string `json:"id"`
}
