package render

import (
	"context"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"iter"
	"randomart/nodes"
	"slices"
	"sync"
)

type pool[J any, R any] struct {
	ctx     context.Context
	cancel  context.CancelFunc
	jobs    chan J
	results chan R
	wg      *sync.WaitGroup
}

func worker[J any, R any](ctx context.Context, wg *sync.WaitGroup, jobs <-chan J, results chan<- R, process func(job J) R) {
	defer wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-jobs:
			if !ok {
				return
			}

			result := process(job)
			select {
			case <-ctx.Done():
				return
			case results <- result:
			}
		}
	}
}

func newPool[J any, R any](ctx context.Context, workers int, process func(job J) R) *pool[J, R] {
	var (
		wg              sync.WaitGroup
		jobs            = make(chan J, workers*10)
		results         = make(chan R, workers*10)
		poolCtx, cancel = context.WithCancel(ctx)
	)
	for _ := range workers {
		wg.Add(1)
		go worker(poolCtx, &wg, jobs, results, process)
	}
	return &pool[J, R]{
		ctx:     poolCtx,
		cancel:  cancel,
		jobs:    jobs,
		results: results,
		wg:      &wg,
	}
}

func (p *pool[J, R]) run(ctx context.Context, job J) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.ctx.Done():
		return p.ctx.Err()
	case p.jobs <- job:
	}
	return nil
}

func (p *pool[J, R]) stop() {
	p.cancel()
	close(p.jobs)
	close(p.results)
}

func (p *pool[J, R]) wait() {
	p.wg.Wait()
}

func renderPoint(root nodes.Node, s nodes.State) (color.Color, error) {
	root, err := root.Eval(s)
	if err != nil {
		return nil, err
	}
	r, g, b, err := nodes.IsRoot(root)
	if err != nil {
		return nil, err
	}
	return color.RGBA{
		R: uint8((r + 1) / 2 * 255),
		G: uint8((g + 1) / 2 * 255),
		B: uint8((b + 1) / 2 * 255),
		A: 255,
	}, nil
}

func points(width, height int) iter.Seq2[int, int] {
	return func(yield func(int, int) bool) {
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				if !yield(x, y) {
					return
				}
			}
		}
	}
}

type frameResult struct {
	frame int
	img   image.Image
	err   error
}

func frames(ctx context.Context, root nodes.Node, options *renderOptions) iter.Seq2[image.Image, error] {
	return func(yield func(image.Image, error) bool) {
		framePool := newPool(ctx, max(options.frames, 10), func(frame int) frameResult {
			img := image.NewRGBA(image.Rect(0, 0, options.width, options.height))
			for x, y := range points(options.width, options.height) {
				src := options.src.At(x, y)
				c, err := renderPoint(root, nodes.S(
					x, y,
					options.width, options.height,
					frame, options.frames,
					src,
				))
				if err != nil {
					return frameResult{frame: frame, err: err}
				}
				img.Set(x, y, c)
			}
			return frameResult{frame: frame, img: img}
		})
		defer func() {
			framePool.stop()
			framePool.wait()
		}()

		go func() {
			for frame := range options.frames {
				if err := framePool.run(ctx, frame); err != nil {
					return
				}
			}
		}()

		var (
			expectedFrame int
			buf           []frameResult
		)
		sortBuf := func() {
			slices.SortFunc(buf, func(a, b frameResult) int {
				return a.frame - b.frame
			})
		}
		for {
			select {
			case <-ctx.Done():
			case result, ok := <-framePool.results:
				if !ok {
					yield(nil, fmt.Errorf("frame result channel closed"))
					return
				}

				if result.err != nil {
					yield(nil, result.err)
					return
				}

				if result.frame != expectedFrame {
					buf = append(buf, result)
					sortBuf()
				} else {
					buf = []frameResult{}
				}
			}
		}
	}
}

type renderOptions struct {
	width  int
	height int
	frames int
	src    image.Image
}

func (r *renderOptions) apply(opts []RenderOption) (*renderOptions, error) {
	for _, opt := range opts {
		if err := opt(r); err != nil {
			return r, err
		}
	}
	if _, ok := r.src.(*image.Uniform); !ok {
		r.width = r.src.Bounds().Dx()
		r.height = r.src.Bounds().Dy()
	}
	if r.frames <= 0 {
		return r, fmt.Errorf("number of frames cannot be negative")
	}
	if r.width <= 0 {
		return r, fmt.Errorf("width cannot be negative")
	}
	if r.height <= 0 {
		return r, fmt.Errorf("height cannot be negative")
	}
	return r, nil
}

func defaultRenderOptions() *renderOptions {
	return &renderOptions{
		width:  400,
		height: 400,
		frames: 1,
		src:    image.NewUniform(color.White),
	}
}

type RenderOption func(options *renderOptions) error

func WithResolution(width, height int) RenderOption {
	return func(options *renderOptions) error {
		options.width = width
		options.height = height
		return nil
	}
}

func WithFrames(frames int) RenderOption {
	return func(options *renderOptions) error {
		options.frames = frames
		return nil
	}
}

func WithSourceImage(r io.Reader) RenderOption {
	return func(options *renderOptions) error {
		var err error
		options.src, _, err = image.Decode(r)
		return err
	}
}

func Render(ctx context.Context, root nodes.Node, opts ...RenderOption) (image.Image, error) {
	options, err := defaultRenderOptions().apply(opts)
	if err != nil {
		return nil, err
	}

	next, stop := iter.Pull2(frames(ctx, root, options))
	defer stop()
	img, err, _ := next()
	return img, err
}

func RenderCallback(ctx context.Context, root nodes.Node, callback func(no int, img image.Image) error, opts ...RenderOption) error {
	options, err := defaultRenderOptions().apply(opts)
	if err != nil {
		return err
	}

	var (
		frameNo int
		frame   image.Image
	)
	for frame, err = range frames(ctx, root, options) {
		if err != nil {
			return err
		}
		if err = callback(frameNo, frame); err != nil {
			return err
		}
		frameNo++
	}
	return nil
}
