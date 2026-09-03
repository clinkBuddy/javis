//go:build windows

package winproc

import "testing"

// A real application was observed answering POST /actuator/shutdown, an
// endpoint it does not have, with status 200 and an HTML error page. Trusting
// that reply cost a full grace period on every stop, so these cases pin down
// what counts as a genuine acknowledgement.
func TestShutdownAccepted(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		contentType string
		wantErr     bool
	}{
		{"actuator json", 200, "application/json", false},
		{"actuator vendor json", 200, "application/vnd.spring-boot.actuator.v3+json", false},
		{"json with charset", 200, "application/json;charset=UTF-8", false},

		{"html error page with 200", 200, "text/html;charset=UTF-8", true},
		{"plain text", 200, "text/plain", true},
		{"no content type", 200, "", true},
		{"not found", 404, "application/json", true},
		{"unauthorised", 401, "application/json", true},
		{"server error", 500, "application/json", true},
		{"malformed content type", 200, "application/", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := shutdownAccepted(tc.status, tc.contentType)
			if tc.wantErr && err == nil {
				t.Errorf("status %d with %q was accepted, want rejection", tc.status, tc.contentType)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("status %d with %q was rejected: %v", tc.status, tc.contentType, err)
			}
		})
	}
}
