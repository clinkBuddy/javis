package api

import (
	"strings"
	"testing"
)

func TestValidateProfileAcceptsTypicalTuning(t *testing.T) {
	req := profileUpdateRequest{
		HeapMin: "512m",
		HeapMax: "2g",
		GC:      "G1",
		JVMArgs: []string{
			"-Dspring.profiles.active=prod",
			"-XX:MaxMetaspaceSize=256m",
			"-Dfile.encoding=UTF-8",
		},
		ProgramArgs: []string{"--server.port=8080"},
		Env:         map[string]string{"TZ": "Asia/Seoul"},
	}

	if err := validateProfile(&req); err != nil {
		t.Fatalf("validateProfile: %v", err)
	}
}

func TestValidateProfileRejectsBadHeapSizes(t *testing.T) {
	for _, size := range []string{"512mb", "2 g", "-1g", "large", "1.5g"} {
		req := profileUpdateRequest{HeapMax: size}
		if err := validateProfile(&req); err == nil {
			t.Errorf("heapMax %q was accepted", size)
		}
	}
}

// -XX:+Use<name>GC is assembled from this field, so an unknown collector would
// produce a flag that aborts the JVM at startup with no clue as to why.
func TestValidateProfileRejectsUnknownCollector(t *testing.T) {
	req := profileUpdateRequest{GC: "Epsilon"}

	err := validateProfile(&req)
	if err == nil {
		t.Fatal("unknown collector was accepted")
	}
	if !strings.Contains(err.Error(), "G1") {
		t.Errorf("error does not list the supported collectors: %v", err)
	}
}

// The jarvis.app and jarvis.instance properties are how a process is
// identified after a JARVIS restart. A duplicate supplied by hand would make
// two processes answer to one instance id.
func TestValidateProfileRejectsReservedMarkerProperties(t *testing.T) {
	for _, arg := range []string{
		"-Djarvis.app=impostor",
		"-Djarvis.instance=00000000000000000000000000000000",
	} {
		req := profileUpdateRequest{JVMArgs: []string{arg}}
		if err := validateProfile(&req); err == nil {
			t.Errorf("reserved marker %q was accepted", arg)
		}
	}
}

func TestValidateProfileRejectsJarFlag(t *testing.T) {
	req := profileUpdateRequest{JVMArgs: []string{"-jar", "other.jar"}}

	if err := validateProfile(&req); err == nil {
		t.Error("-jar in jvmArgs was accepted")
	}
}

func TestValidateProfileRejectsLineBreaksInArgs(t *testing.T) {
	req := profileUpdateRequest{JVMArgs: []string{"-Xss512k\n-Dfoo=bar"}}

	if err := validateProfile(&req); err == nil {
		t.Error("an argument containing a newline was accepted")
	}
}

func TestValidateProfileRejectsEmptyArgEntry(t *testing.T) {
	req := profileUpdateRequest{JVMArgs: []string{"-Xss512k", "   "}}

	if err := validateProfile(&req); err == nil {
		t.Error("a blank argument entry was accepted")
	}
}

func TestValidateProfileRejectsMalformedEnvKeys(t *testing.T) {
	for _, key := range []string{"", "A=B", "WITH\nNEWLINE"} {
		req := profileUpdateRequest{Env: map[string]string{key: "value"}}
		if err := validateProfile(&req); err == nil {
			t.Errorf("env key %q was accepted", key)
		}
	}
}

func TestQuoteCommandLineQuotesOnlyWhatNeedsIt(t *testing.T) {
	got := quoteCommandLine([]string{
		`C:\Program Files\Java\jdk-21\bin\java.exe`,
		"-Xmx2g",
		"-jar",
		`C:\ProgramData\JARVIS\repo\garage\0.1.0\app.jar`,
	})

	want := `"C:\Program Files\Java\jdk-21\bin\java.exe" -Xmx2g -jar C:\ProgramData\JARVIS\repo\garage\0.1.0\app.jar`
	if got != want {
		t.Errorf("quoteCommandLine()\n got: %s\nwant: %s", got, want)
	}
}
