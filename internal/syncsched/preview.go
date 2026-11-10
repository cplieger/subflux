package syncsched

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/statedb"
)

// previewType is the content type of every preview stream.
const previewType = "video/mp4"

// Preview streams inst's video from from as ffmpeg.Runner.Preview cuts it,
// with its content type; false when the store holds no inst. from outside
// 0 to 24 h is ErrPreviewRange, and MaxPreviews streams already open
// ErrPreviewCapacity. Closing the stream ends ffmpeg and frees its place;
// the matching subtitle track is timing.RenderVTT's with the same from.
func (a *Aligner) Preview(ctx context.Context, inst statedb.InstanceID, from time.Duration) (stream io.ReadCloser, contentType string, found bool, err error) {
	if from < 0 || from > maxPreviewFrom {
		return nil, "", false, fmt.Errorf("%w: %v", ErrPreviewRange, from)
	}
	a.mu.Lock()
	opened := a.ledger.openPreview()
	a.mu.Unlock()
	if !opened {
		return nil, "", false, fmt.Errorf("%w: %d previews streaming", ErrPreviewCapacity, MaxPreviews)
	}
	release := sync.OnceFunc(func() {
		a.mu.Lock()
		a.ledger.closePreview()
		a.mu.Unlock()
	})
	video, err := a.writer.OpenVideo(ctx, inst)
	if err != nil {
		release()
		if errors.Is(err, mediawrite.ErrInvalidRef) {
			return nil, "", false, nil
		}
		return nil, "", false, err
	}
	// The child holds its own descriptor once started.
	stream, err = a.run.Preview(ctx, video, from)
	_ = video.Close()
	if err != nil {
		release()
		return nil, "", false, err
	}
	return &preview{ReadCloser: stream, release: release}, previewType, true, nil
}

// preview frees its place in the bound once closed.
type preview struct {
	io.ReadCloser
	release func()
}

func (p *preview) Close() error {
	err := p.ReadCloser.Close()
	p.release()
	return err
}
