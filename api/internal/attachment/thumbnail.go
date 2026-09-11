package attachment

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"  // register the GIF decoder for image.Decode
	"image/jpeg"   // decoder + the thumbnail encoder
	_ "image/png"  // register the PNG decoder for image.Decode
	"io"
)

const (
	// thumbMaxDim caps the thumbnail's longest side. 256px is enough for a crisp
	// grid tile on both the web panel and a field tablet, while staying tiny
	// enough that every image thumbnail can ride down inside a field pack.
	thumbMaxDim = 256

	// thumbJPEGQuality trades a little fidelity for a much smaller thumbnail --
	// these are previews, not the archival image (that stays full-res in RustFS).
	thumbJPEGQuality = 80
)

// Thumbnail decodes an image from r and returns a downscaled JPEG whose longest
// side is at most thumbMaxDim, for offline display in a field pack and quick
// previews on the web. It is pure standard library: image.Decode handles
// JPEG/PNG/GIF (decoders registered by the blank imports above), a box
// downsample averages the source pixels covered by each destination pixel, and
// the result is re-encoded as JPEG. It never upscales -- a small source is
// re-encoded at its own size.
//
// A non-image (e.g. a PDF a web user attaches as a document) fails to decode and
// returns an error, so callers use that to decide a row gets no thumb_key rather
// than treating it as fatal.
func Thumbnail(r io.Reader) ([]byte, error) {
	src, _, err := image.Decode(r)
	if err != nil {
		return nil, fmt.Errorf("decoding image for thumbnail: %w", err)
	}
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw <= 0 || sh <= 0 {
		return nil, fmt.Errorf("image has a zero dimension")
	}

	// Normalize to a single RGBA buffer once, so downsampling reads pixels via a
	// flat byte slice rather than the decoded image's (possibly YCbCr/paletted)
	// color model on every sample.
	rgba := image.NewRGBA(image.Rect(0, 0, sw, sh))
	draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Src)

	tw, th := thumbBounds(sw, sh, thumbMaxDim)
	dst := boxDownsample(rgba, tw, th)

	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: thumbJPEGQuality}); err != nil {
		return nil, fmt.Errorf("encoding thumbnail: %w", err)
	}
	return out.Bytes(), nil
}

// thumbBounds returns the target size that fits (sw x sh) within a max x max box
// while preserving aspect ratio, never upscaling. The shorter side is rounded
// toward the source ratio and floored at 1 so a very oblong image still yields a
// valid (non-zero) dimension.
func thumbBounds(sw, sh, max int) (int, int) {
	if sw <= max && sh <= max {
		return sw, sh
	}
	if sw >= sh {
		h := sh * max / sw
		if h < 1 {
			h = 1
		}
		return max, h
	}
	w := sw * max / sh
	if w < 1 {
		w = 1
	}
	return w, max
}

// boxDownsample averages the block of source pixels mapping to each destination
// pixel -- a straightforward box filter that gives clean thumbnails without any
// third-party resize dependency. Same-size is a fast copy. Alpha is averaged too
// though JPEG drops it; keeping it correct means the same routine could feed a
// PNG thumbnail later without change.
func boxDownsample(src *image.RGBA, tw, th int) *image.RGBA {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, tw, th))
	if tw == sw && th == sh {
		copy(dst.Pix, src.Pix)
		return dst
	}
	for dy := 0; dy < th; dy++ {
		sy0 := dy * sh / th
		sy1 := (dy + 1) * sh / th
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for dx := 0; dx < tw; dx++ {
			sx0 := dx * sw / tw
			sx1 := (dx + 1) * sw / tw
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			var rSum, gSum, bSum, aSum, count uint32
			for sy := sy0; sy < sy1; sy++ {
				rowStart := src.PixOffset(sx0, sy)
				for i, sx := rowStart, sx0; sx < sx1; i, sx = i+4, sx+1 {
					rSum += uint32(src.Pix[i])
					gSum += uint32(src.Pix[i+1])
					bSum += uint32(src.Pix[i+2])
					aSum += uint32(src.Pix[i+3])
					count++
				}
			}
			di := dst.PixOffset(dx, dy)
			dst.Pix[di] = uint8(rSum / count)
			dst.Pix[di+1] = uint8(gSum / count)
			dst.Pix[di+2] = uint8(bSum / count)
			dst.Pix[di+3] = uint8(aSum / count)
		}
	}
	return dst
}
