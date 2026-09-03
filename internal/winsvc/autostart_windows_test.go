//go:build windows

package winsvc

import "testing"

// The first version of EnableTrayAutostart used fmt's %q and wrote
// "C:\\dev\\jarvis\\bin\\jarvis.exe" into the Run key. These cases pin the
// Windows quoting rules so that regression cannot come back.
func TestQuoteArg(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain path", `C:\dev\jarvis\bin\jarvis.exe`, `"C:\dev\jarvis\bin\jarvis.exe"`},
		{"spaces", `C:\Program Files\JARVIS\jarvis.exe`, `"C:\Program Files\JARVIS\jarvis.exe"`},
		{"program data", `C:\ProgramData\JARVIS`, `"C:\ProgramData\JARVIS"`},
		{"empty", ``, `""`},
		{"unc path", `\\server\share\jarvis.exe`, `"\\server\share\jarvis.exe"`},

		// A trailing backslash must be doubled or it escapes the closing quote.
		{"trailing backslash", `C:\data\`, `"C:\data\\"`},
		{"two trailing backslashes", `C:\data\\`, `"C:\data\\\\"`},

		// Embedded quotes: N backslashes before a quote become 2N+1.
		{"embedded quote", `a"b`, `"a\"b"`},
		{"backslash then quote", `a\"b`, `"a\\\"b"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := quoteArg(tc.in); got != tc.want {
				t.Errorf("quoteArg(%#q)\n got %s\nwant %s", tc.in, got, tc.want)
			}
		})
	}
}
