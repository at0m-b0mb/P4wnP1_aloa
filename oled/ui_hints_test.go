package oled

import "testing"

// A hint that does not fit is a hint that ends in "~", which on the one line
// explaining what the unlabelled keys do is worse than useless. Two of them
// shipped truncated before this existed.
func TestEveryHintFitsTheScreen(t *testing.T) {
	c := NewFakeClient()
	app := NewApp(c, NewRoot())
	views := []View{
		NewRoot(), NewStatusView(), NewUSBView(), NewPayloadList(), NewJobsView(),
		NewRadioMenu(), NewSystemMenu(),
		NewStoredList(KindMasterTemplate), NewStoredList(KindWifiSettings),
		NewStoredList(KindDBBackup),
		NewConfirm("T", "Question?", "Detail.", nil),
		NewTextView("T", "body"),
	}
	for _, v := range views {
		h := v.Hint()
		if n := len([]rune(h)); n > Cols-1 {
			t.Errorf("%T hint %q is %d chars, max %d", v, h, n, Cols-1)
		}
	}
	// Titles share the bar with a back arrow.
	for _, v := range views {
		if n := len([]rune(v.Title())); n > Cols-1 {
			t.Errorf("%T title %q is %d chars, max %d", v, v.Title(), n, Cols-1)
		}
	}
	_ = app
}
