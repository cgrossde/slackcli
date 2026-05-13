// Package slack — auth.go implements auth.test via slack-go.
package slack

// AuthTestResult is the result of an auth.test call.
type AuthTestResult struct {
	OK           bool
	URL          string
	Team         string
	User         string
	TeamID       string
	UserID       string
	EnterpriseID string
	// Error is populated when OK is false.
	Error string
}

// AuthTest calls auth.test and returns the result.
// Returns a non-nil error only on transport or parse failure.
// When the token is invalid or expired, AuthTestResult.OK is false and
// AuthTestResult.Error contains the reason (e.g. "invalid_auth").
func (c *Client) AuthTest() (AuthTestResult, error) {
	resp, err := c.api.AuthTest()
	if err != nil {
		// slack-go returns the Slack error code as the error string when the
		// API call itself succeeds but ok=false. Treat that as a logical
		// failure (OK=false) rather than a transport error so callers can
		// distinguish "bad token" from "network down".
		return AuthTestResult{OK: false, Error: err.Error()}, nil
	}
	return AuthTestResult{
		OK:           true,
		URL:          resp.URL,
		Team:         resp.Team,
		User:         resp.User,
		TeamID:       resp.TeamID,
		UserID:       resp.UserID,
		EnterpriseID: resp.EnterpriseID,
	}, nil
}
