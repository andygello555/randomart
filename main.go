package main

import (
	"context"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/signal"
	"path"
	"randomart/nodes"
	"randomart/render"
	"strings"
	"syscall"
)

var (
	grammarFilename       = flag.String("grammar", "grammar.bnf", "Path to the grammar file to generate from")
	outputFilename        = flag.String("output", "output.png", "Path to output file that the randomart will be written to")
	width                 = flag.Int("width", 400, "The width of the produced randomart")
	height                = flag.Int("height", 400, "The height of the produced randomart")
	frames                = flag.Int("frames", 1, "The number of frames of randomart to generate")
	srcFilename           = flag.String("src", "", "Path to the source image to use as a starting point for the randomart algorithm")
	optionsOutputFilename = flag.String("ooptions", "", "Path to output generator options to so that the randomart image can be reproduced")
	optionsInputFilename  = flag.String("ioptions", "", "Path to a JSON file containing options to pass to the generator")
	verbose               = flag.Bool("verbose", false, "Output more logs")
)

func main() {
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sig := make(chan os.Signal, 1)
	go func() {
		defer cancel()
		select {
		case <-ctx.Done():
		case <-sig:
			fmt.Println("Received interrupt...")
		}
	}()
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	grammarFile, err := os.Open(*grammarFilename)
	if err != nil {
		fmt.Printf("could not open grammar file %q: %s\n", *grammarFilename, err)
		return
	}
	defer grammarFile.Close()

	grammar, err := nodes.Parse(grammarFile, *grammarFilename)
	if err != nil {
		fmt.Printf("could not parse grammar: %s\n", err)
		return
	}
	fmt.Println(grammar.String())

	var genOpts []nodes.GeneratorOption
	if *optionsInputFilename != "" {
		optionsInputFile, err := os.Open(*optionsInputFilename)
		if err != nil {
			fmt.Printf("could not open input options file %q: %s\n", *optionsInputFilename, err)
			return
		}
		defer optionsInputFile.Close()
		genOpts = append(genOpts, nodes.FromJSON(optionsInputFile))
	}

	node, state, err := grammar.Gen(genOpts...)
	if err != nil {
		fmt.Printf("could not generate random AST: %s\n", err)
		return
	}
	options := state.Options()
	fmt.Println(node)
	fmt.Println(options)

	renOpts := []render.RenderOption{
		render.WithResolution(*width, *height),
		render.WithFrames(*frames),
	}
	if *srcFilename != "" {
		srcFile, err := os.Open(*srcFilename)
		if err != nil {
			fmt.Printf("could not open src file %q: %s\n", *srcFilename, err)
			return
		}
		defer srcFile.Close()
		renOpts = append(renOpts, render.WithSourceImage(srcFile))
	}
	if *verbose {
		renOpts = append(renOpts, render.WithLogger(func(f string, args ...any) {
			fmt.Printf(f, args...)
		}))
	}

	err = render.RenderCallback(ctx, node, func(no int, img image.Image) error {
		filename := *outputFilename
		if *frames > 1 {
			ext := path.Ext(filename)
			filename = fmt.Sprintf("%s-%03d%s", strings.TrimSuffix(filename, ext), no, ext)
		}

		fmt.Printf("rendering frame %d to %s... ", no, filename)
		defer fmt.Println("Done!")

		out, err := os.Create(filename)
		if err != nil {
			return fmt.Errorf("could not open output file %q for frame %d: %w", filename, no, err)
		}
		defer out.Close()

		if err = png.Encode(out, img); err != nil {
			return fmt.Errorf("could not write PNG for frame %d: %w", no, err)
		}
		return nil
	}, renOpts...)
	if err != nil {
		fmt.Printf("could not render image: %s\n", err)
		return
	}

	if *optionsOutputFilename != "" {
		optionsOutputFile, err := os.Create(*optionsOutputFilename)
		if err != nil {
			fmt.Printf("could not open output options file %q: %s\n", *optionsOutputFilename, err)
			return
		}
		defer optionsOutputFile.Close()

		_, err = optionsOutputFile.WriteString(options)
		if err != nil {
			fmt.Printf("could not write generator options to file: %s\n", err)
			return
		}
	}
}
