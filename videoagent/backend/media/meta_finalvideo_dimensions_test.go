package media

import "testing"

func TestFinalVideoUpscaleSizeFollowsOrientation(t *testing.T) {
	width, height := finalVideoUpscaleSize(720, 1280)
	if width != 1080 || height != 1920 {
		t.Fatalf("portrait upscale = %dx%d, want 1080x1920", width, height)
	}
	width, height = finalVideoUpscaleSize(1280, 720)
	if width != 1920 || height != 1080 {
		t.Fatalf("landscape upscale = %dx%d, want 1920x1080", width, height)
	}
}
