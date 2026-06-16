package bus

import "testing"

func TestPaneShowsProviderLoop_Detects(t *testing.T) {
	cases := []string{
		`Type validation failed: Value: {"code":"InvalidParameter","message":"<400> InternalError.Algo. ...`,
		"identical name and arguments has been repeated across multiple consecutive rounds. Please modify",
		`"note": "No matching discriminator", "discriminator": "type"`,
		"please adjust the tool call arguments to avoid infinite loops",
	}
	for _, c := range cases {
		if !PaneShowsProviderLoop(c) {
			t.Errorf("expected provider-loop detection for: %q", c)
		}
	}
}

func TestPaneShowsProviderLoop_CaseInsensitive(t *testing.T) {
	if !PaneShowsProviderLoop("INTERNALERROR.ALGO rejected the request") {
		t.Error("detection should be case-insensitive")
	}
}

func TestPaneShowsProviderLoop_CleanPaneIgnored(t *testing.T) {
	clean := []string{
		"",
		"=== RUN TestFoo\n--- PASS: TestFoo (0.00s)\nok  example  0.1s",
		"❯ ready for next request",
		"Build succeeded: 2 modules compiled",
	}
	for _, c := range clean {
		if PaneShowsProviderLoop(c) {
			t.Errorf("clean pane should not match: %q", c)
		}
	}
}
