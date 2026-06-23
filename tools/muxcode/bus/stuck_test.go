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

func TestPaneShowsPermissionBlock_Detects(t *testing.T) {
	cases := []string{
		"Execution of ./build.sh blocked by permission system. Awaiting authorization.",
		"rejected. Unable to proceed without explicit user authorization.",
		"The command was blocked by the permission system",
		"Cannot run: blocked by permission system",
	}
	for _, c := range cases {
		if !PaneShowsPermissionBlock(c) {
			t.Errorf("expected permission-block detection for: %q", c)
		}
	}
}

func TestPaneShowsPermissionBlock_CaseInsensitive(t *testing.T) {
	if !PaneShowsPermissionBlock("BLOCKED BY PERMISSION SYSTEM — awaiting approval") {
		t.Error("detection should be case-insensitive")
	}
}

func TestPaneShowsPermissionBlock_CleanPaneIgnored(t *testing.T) {
	clean := []string{
		"",
		"❯ ready for next request",
		"Build succeeded: 2 modules compiled",
		// Mentions permissions but is not a denial — must not false-positive.
		"Updated the permission profile for the build agent",
		"Checking file permissions before write",
	}
	for _, c := range clean {
		if PaneShowsPermissionBlock(c) {
			t.Errorf("clean pane should not match: %q", c)
		}
	}
}
