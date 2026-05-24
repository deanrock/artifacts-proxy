package utils

import (
	"net/http"
	"testing"
)

func TestGetRequestIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                 string
		xForwardedFor        string
		xForwardedForEnabled bool
		expected             string
	}{
		{"xForwardedFor should be used", "3.3.3.3", true, "3.3.3.3"},
		{"last xForwardedFor should be used", "1.1.1.1, 2.2.2.2, 3.3.3.3", true, "3.3.3.3"},
		{"remoteAddr should be used", "1.1.1.1, 2.2.2.2, 3.3.3.3", false, "9.9.9.9"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			r, _ := http.NewRequest("GET", "/", nil)
			r.RemoteAddr = "9.9.9.9:80"
			r.Header.Add("X-Forwarded-For", test.xForwardedFor)

			result, err := GetRequestIP(test.xForwardedForEnabled, r)

			if err != nil {
				t.Errorf("failed with error: %s", err)
			}

			if result != test.expected {
				t.Errorf("expected %s, got %s", test.expected, result)
			}
		})
	}
}
