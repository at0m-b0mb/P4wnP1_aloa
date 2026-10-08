package oled

import "testing"

// What the mirror costs the device, measured rather than asserted.
//
// publish() runs on every repaint -- 6.7 times a second -- so it must be
// trivial. ReadBack is the expensive one and now runs only when a browser
// asks, at most twice a second and only while someone is watching.
func BenchmarkPublishCopy(b *testing.B) {
	fb := NewFramebuffer()
	app := NewApp(NewFakeClient(), NewRoot())
	app.Render(fb)
	dst := make([]byte, len(fb.Bytes()))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		copy(dst, fb.Bytes())
	}
}

func BenchmarkReadBack(b *testing.B) {
	fb := NewFramebuffer()
	app := NewApp(NewFakeClient(), NewRoot())
	app.Render(fb)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ReadBack(fb)
	}
}

func BenchmarkRender(b *testing.B) {
	fb := NewFramebuffer()
	app := NewApp(NewFakeClient(), NewRoot())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		app.Render(fb)
	}
}
