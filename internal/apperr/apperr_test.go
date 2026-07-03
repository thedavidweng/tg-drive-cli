package apperr

import "testing"

func TestExitCodeMapping(t *testing.T) {
	cases := []struct {
		code string
		want int
	}{
		{ErrUsage, 2},
		{ErrFlagConflict, 2},
		{ErrPathInvalid, 2},
		{ErrPathExists, 2},
		{ErrLocalPathExists, 2},
		{ErrLocalNotFound, 2},
		{ErrRemoteNotFound, 2},
		{ErrDirectoryMoveUnsupported, 2},
		{ErrDirectoryDeleteUnsupported, 2},
		{ErrSlugCollision, 2},
		{ErrAuthRequired, 3},
		{ErrConfigMissing, 3},
		{ErrConfigInvalid, 3},
		{ErrChannelNotFound, 4},
		{ErrChannelPermission, 4},
		{ErrFileTooLarge, 4},
		{ErrMessageNotEditable, 4},
		{ErrTelegramRateLimited, 4},
		{ErrTelegramRPC, 4},
		{ErrDB, 5},
		{ErrScanFailed, 5},
		{ErrManifestInvalid, 5},
		{ErrOperationLocked, 5},
		{ErrRepairRequired, 5},
		{ErrCaptionTooLong, 5},
	}
	for _, tc := range cases {
		got := ExitCode(New(tc.code, "test"))
		if got != tc.want {
			t.Errorf("ExitCode(%s) = %d, want %d", tc.code, got, tc.want)
		}
	}
}

func TestExitCodeUncategorized(t *testing.T) {
	if got := ExitCode(New("ERR_UNKNOWN", "x")); got != 1 {
		t.Fatalf("uncategorized exit = %d, want 1", got)
	}
	if got := ExitCode(nil); got != 1 {
		t.Fatalf("nil exit = %d, want 1", got)
	}
}
