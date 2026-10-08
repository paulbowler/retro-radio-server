// SPDX-License-Identifier: GPL-3.0-only
package artwork

import (
	"bytes"
	"image"
	"image/jpeg"
	"testing"
)

func TestLargePrivateAlbumCoverWithoutRelaxingPublicLimit(t *testing.T) {
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, image.NewGray(image.Rect(0, 0, 2850, 2850)), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := RadioJPEG(encoded.Bytes()); err == nil {
		t.Fatal("public cover limit relaxed")
	}
	data, err := AlbumJPEG(encoded.Bytes())
	if err != nil {
		t.Fatal("Ultimate-sized private cover rejected", err)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width != 128 || config.Height != 128 {
		t.Fatal(config, err)
	}
}
func TestPrivateCoverStillBoundsDimensionsAndRejectsInvalidImages(t *testing.T) {
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, image.NewGray(image.Rect(0, 0, 4097, 1)), nil); err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{encoded.Bytes(), []byte("not an image")} {
		if _, err := AlbumJPEG(data); err == nil {
			t.Fatal("invalid cover accepted")
		}
	}
}
