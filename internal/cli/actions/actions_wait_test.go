package actions

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func newWaitCmd() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().String("text", "", "")
	cmd.Flags().String("not-text", "", "")
	cmd.Flags().String("url", "", "")
	cmd.Flags().String("load", "", "")
	cmd.Flags().String("fn", "", "")
	cmd.Flags().String("state", "", "")
	cmd.Flags().String("tab", "", "")
	cmd.Flags().Int("timeout", 0, "")
	cmd.Flags().Bool("json", false, "")
	return cmd
}

const waitChildEnv = "PINCHTAB_TEST_WAIT_MODE"

// runWaitChild serves a /wait response for the case named by mode ("met"/"timeout"
// prefix, "json" suffix) and drives Wait against it. A met wait returns normally,
// so it exits 0 explicitly — without that the test framework's trailing "PASS"
// output would land on stdout and break the single-JSON-object assertion. A timed
// out wait never reaches the exit here: Wait's output.Error already os.Exit(4)s.
func runWaitChild(mode string) {
	waited := strings.HasPrefix(mode, "met")
	jsonMode := strings.HasSuffix(mode, "json")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]any{"waited": waited, "elapsed": 1000}
		if !waited {
			resp["error"] = "timeout after 1000ms waiting for selector"
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cmd := newWaitCmd()
	if jsonMode {
		_ = cmd.Flags().Set("json", "true")
	}
	_ = cmd.Flags().Set("timeout", "1000")
	Wait(srv.Client(), srv.URL, "", []string{"#neverever"}, cmd)
	os.Exit(0)
}

// The whole point of the card, asserted as exit codes: a met wait exits 0 and a
// timed-out wait exits ExitTimeout (4) — in BOTH terse and --json modes, so a
// script gating on the exit status keeps working when it adds --json. Driven in a
// child process because Wait's timeout path calls os.Exit.
func TestWait_ExitCodeTracksOutcomeNotTheFlag(t *testing.T) {
	if mode := os.Getenv(waitChildEnv); mode != "" {
		runWaitChild(mode)
		return
	}

	for _, tc := range []struct {
		mode     string
		wantExit int
		json     bool
	}{
		{"met-terse", 0, false},
		{"met-json", 0, true},
		{"timeout-terse", 4, false},
		{"timeout-json", 4, true},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			child := exec.Command(os.Args[0], "-test.run=TestWait_ExitCodeTracksOutcomeNotTheFlag", "-test.timeout=60s") // #nosec G204 -- re-executes this test binary.
			child.Env = append(os.Environ(), waitChildEnv+"="+tc.mode)
			var stdout, stderr bytes.Buffer
			child.Stdout = &stdout
			child.Stderr = &stderr

			err := child.Run()
			exitCode := 0
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				exitCode = exitErr.ExitCode()
			} else if err != nil {
				t.Fatalf("child failed to run: %v (stderr: %s)", err, stderr.String())
			}

			if exitCode != tc.wantExit {
				t.Fatalf("%s exit = %d, want %d (stdout=%q stderr=%q)", tc.mode, exitCode, tc.wantExit, stdout.String(), stderr.String())
			}

			if tc.json {
				// AC: --json prints exactly one full JSON object, jq-parseable, before exiting.
				dec := json.NewDecoder(strings.NewReader(stdout.String()))
				var obj map[string]any
				if err := dec.Decode(&obj); err != nil {
					t.Fatalf("%s stdout is not one JSON object: %v\nstdout=%q", tc.mode, err, stdout.String())
				}
				if dec.More() {
					t.Fatalf("%s stdout has content after the JSON object:\nstdout=%q", tc.mode, stdout.String())
				}
				if _, ok := obj["waited"].(bool); !ok {
					t.Errorf("%s JSON object lacks a boolean waited field: %s", tc.mode, stdout.String())
				}
			} else if strings.Contains(stdout.String(), "{") {
				t.Errorf("%s terse stdout must not carry a JSON object: %q", tc.mode, stdout.String())
			}
		})
	}
}
