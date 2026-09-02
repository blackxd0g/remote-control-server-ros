package httpapi

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func TestValidateAvatar(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	mediaType, err := validateAvatar(encoded.Bytes())
	if err != nil || mediaType != "image/png" {
		t.Fatalf("expected valid PNG avatar, got %q, %v", mediaType, err)
	}
	if _, err = validateAvatar([]byte("not an image")); err == nil {
		t.Fatal("expected non-image data to be rejected")
	}
}

func TestValidAvatarIDRejectsTraversal(t *testing.T) {
	for _, value := range []string{"../secret", "user/name", "", "user%2Fname"} {
		if validAvatarID(value) {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
	if !validAvatarID("123e4567-e89b-12d3-a456-426614174000") || !validAvatarID("global") {
		t.Fatal("expected safe avatar identifiers to be accepted")
	}
}
