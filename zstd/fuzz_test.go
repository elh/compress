//go:build go1.18
// +build go1.18

package zstd

import (
	"bytes"
	"fmt"
	"io"
	rdebug "runtime/debug"
	"testing"

	"github.com/klauspost/compress/internal/cpuinfo"
	"github.com/klauspost/compress/internal/fuzz"
)

func FuzzDecodeAll(f *testing.F) {
	fuzz.AddFromZip(f, "testdata/fuzz/decode-corpus-raw.zip", true, testing.Short())
	fuzz.AddFromZip(f, "testdata/fuzz/decode-corpus-encoded.zip", false, testing.Short())
	decLow, err := NewReader(nil, WithDecoderLowmem(true), WithDecoderConcurrency(2), WithDecoderMaxMemory(20<<20), WithDecoderMaxWindow(1<<20), IgnoreChecksum(true))
	if err != nil {
		f.Fatal(err)
	}
	defer decLow.Close()
	decHi, err := NewReader(nil, WithDecoderLowmem(false), WithDecoderConcurrency(2), WithDecoderMaxMemory(20<<20), WithDecoderMaxWindow(1<<20), IgnoreChecksum(true))
	if err != nil {
		f.Fatal(err)
	}
	defer decHi.Close()

	f.Fuzz(func(t *testing.T, b []byte) {
		// Just test if we crash...
		defer func() {
			if r := recover(); r != nil {
				rdebug.PrintStack()
				t.Fatal(r)
			}
		}()
		b1, err1 := decLow.DecodeAll(b, nil)
		b2, err2 := decHi.DecodeAll(b, nil)
		if err1 != err2 {
			t.Log(err1, err2)
		}
		if err1 != nil {
			b1, b2 = b1[:0], b2[:0]
		}
		if !bytes.Equal(b1, b2) {
			t.Fatalf("Output mismatch, low: %v, hi: %v", err1, err2)
		}
	})
}

func FuzzDecAllNoBMI2(f *testing.F) {
	if !cpuinfo.HasBMI2() {
		f.Skip("No BMI, so already tested")
		return
	}
	defer cpuinfo.DisableBMI2()()
	FuzzDecodeAll(f)
}

func FuzzDecoder(f *testing.F) {
	fuzz.AddFromZip(f, "testdata/fuzz/decode-corpus-raw.zip", true, testing.Short())
	fuzz.AddFromZip(f, "testdata/fuzz/decode-corpus-encoded.zip", false, testing.Short())
	decLow, err := NewReader(nil, WithDecoderLowmem(true), WithDecoderConcurrency(2), WithDecoderMaxMemory(20<<20), WithDecoderMaxWindow(1<<20), IgnoreChecksum(true), WithDecodeBuffersBelow(8<<10))
	if err != nil {
		f.Fatal(err)
	}
	defer decLow.Close()
	// Test with high memory, but sync decoding
	decHi, err := NewReader(nil, WithDecoderLowmem(false), WithDecoderConcurrency(1), WithDecoderMaxMemory(20<<20), WithDecoderMaxWindow(1<<20), IgnoreChecksum(true), WithDecodeBuffersBelow(8<<10))
	if err != nil {
		f.Fatal(err)
	}
	defer decHi.Close()

	brLow := newBytesReader(nil)
	brHi := newBytesReader(nil)
	f.Fuzz(func(t *testing.T, b []byte) {
		// Just test if we crash...
		defer func() {
			if r := recover(); r != nil {
				rdebug.PrintStack()
				t.Fatal(r)
			}
		}()
		brLow.Reset(b)
		brHi.Reset(b)
		err := decLow.Reset(brLow)
		if err != nil {
			t.Fatal(err)
		}
		err = decHi.Reset(brHi)
		if err != nil {
			t.Fatal(err)
		}
		b1, err1 := io.ReadAll(decLow)
		b2, err2 := io.ReadAll(decHi)
		if err1 != err2 {
			t.Log(err1, err2)
		}
		if err1 != nil {
			b1, b2 = b1[:0], b2[:0]
		}
		if !bytes.Equal(b1, b2) {
			t.Fatalf("Output mismatch, low: %v, hi: %v", err1, err2)
		}
	})
}

func FuzzNoBMI2Dec(f *testing.F) {
	if !cpuinfo.HasBMI2() {
		f.Skip("No BMI, so already tested")
		return
	}
	defer cpuinfo.DisableBMI2()()
	FuzzDecoder(f)
}

func FuzzEncoding(f *testing.F) {
	fuzz.AddFromZip(f, "testdata/fuzz/encode-corpus-raw.zip", true, testing.Short())
	fuzz.AddFromZip(f, "testdata/comp-crashers.zip", true, false)
	fuzz.AddFromZip(f, "testdata/fuzz/encode-corpus-encoded.zip", false, testing.Short())
	// Fuzzing tweaks:
	const (
		// Test a subset of encoders.
		startFuzz = SpeedFastest
		endFuzz   = SpeedBetterCompression

		// Also tests with dictionaries...
		testDicts = true

		// Max input size:
		maxSize = 1 << 20
	)

	var dec *Decoder
	var encs [SpeedBestCompression + 1]*Encoder
	var encsD [SpeedBestCompression + 1]*Encoder

	var dicts [][]byte
	if testDicts {
		zr := testCreateZipReader("testdata/dict-tests-small.zip", f)
		dicts = readDicts(f, zr)
	}

	initEnc := func() func() {
		var err error
		dec, err = NewReader(nil, WithDecoderConcurrency(2), WithDecoderDicts(dicts...), WithDecoderMaxWindow(128<<10), WithDecoderMaxMemory(maxSize))
		if err != nil {
			panic(err)
		}
		for level := startFuzz; level <= endFuzz; level++ {
			encs[level], err = NewWriter(nil, WithEncoderCRC(true), WithEncoderLevel(level), WithEncoderConcurrency(2), WithWindowSize(128<<10), WithZeroFrames(true), WithLowerEncoderMem(true))
			if testDicts {
				encsD[level], err = NewWriter(nil, WithEncoderCRC(true), WithEncoderLevel(level), WithEncoderConcurrency(2), WithWindowSize(128<<10), WithZeroFrames(true), WithEncoderDict(dicts[0]), WithLowerEncoderMem(true), WithLowerEncoderMem(true))
			}
		}
		return func() {
			dec.Close()
			for _, enc := range encs {
				if enc != nil {
					enc.Close()
				}
			}
			if testDicts {
				for _, enc := range encsD {
					if enc != nil {
						enc.Close()
					}
				}
			}
		}
	}

	f.Cleanup(initEnc())

	var dst bytes.Buffer

	f.Fuzz(func(t *testing.T, data []byte) {
		// Just test if we crash...
		defer func() {
			if r := recover(); r != nil {
				rdebug.PrintStack()
				t.Fatal(r)
			}
		}()
		if len(data) > maxSize {
			return
		}
		var bufSize = len(data)
		if bufSize > 2 {
			// Make deterministic size
			bufSize = int(data[0]) | int(data[1])<<8
			if bufSize >= len(data) {
				bufSize = len(data) / 2
			}
		}

		for level := startFuzz; level <= endFuzz; level++ {
			enc := encs[level]
			dst.Reset()
			enc.Reset(&dst)
			n, err := enc.Write(data)
			if err != nil {
				t.Fatal(err)
			}
			if n != len(data) {
				t.Fatal(fmt.Sprintln("Level", level, "Short write, got:", n, "want:", len(data)))
			}

			encoded := enc.EncodeAll(data, make([]byte, 0, bufSize))
			got, err := dec.DecodeAll(encoded, make([]byte, 0, bufSize))
			if err != nil {
				t.Fatal(fmt.Sprintln("Level", level, "DecodeAll error:", err, "\norg:", len(data), "\nencoded", len(encoded)))
			}
			if !bytes.Equal(got, data) {
				t.Fatal(fmt.Sprintln("Level", level, "DecodeAll output mismatch\n", len(got), "org: \n", len(data), "(want)", "\nencoded:", len(encoded)))
			}

			err = enc.Close()
			if err != nil {
				t.Fatal(fmt.Sprintln("Level", level, "Close (buffer) error:", err))
			}
			encoded2 := dst.Bytes()
			if !bytes.Equal(encoded, encoded2) {
				got, err = dec.DecodeAll(encoded2, got[:0])
				if err != nil {
					t.Fatal(fmt.Sprintln("Level", level, "DecodeAll (buffer) error:", err, "\norg:", len(data), "\nencoded", len(encoded2)))
				}
				if !bytes.Equal(got, data) {
					t.Fatal(fmt.Sprintln("Level", level, "DecodeAll (buffer) output mismatch\n", len(got), "org: \n", len(data), "(want)", "\nencoded:", len(encoded2)))
				}
			}
			if !testDicts {
				continue
			}
			enc = encsD[level]
			dst.Reset()
			enc.Reset(&dst)
			n, err = enc.Write(data)
			if err != nil {
				t.Fatal(err)
			}
			if n != len(data) {
				t.Fatal(fmt.Sprintln("Dict Level", level, "Short write, got:", n, "want:", len(data)))
			}

			encoded = enc.EncodeAll(data, encoded[:0])
			got, err = dec.DecodeAll(encoded, got[:0])
			if err != nil {
				t.Fatal(fmt.Sprintln("Dict Level", level, "DecodeAll error:", err, "\norg:", len(data), "\nencoded", len(encoded)))
			}
			if !bytes.Equal(got, data) {
				t.Fatal(fmt.Sprintln("Dict Level", level, "DecodeAll output mismatch\n", len(got), "org: \n", len(data), "(want)", "\nencoded:", len(encoded)))
			}

			err = enc.Close()
			if err != nil {
				t.Fatal(fmt.Sprintln("Dict Level", level, "Close (buffer) error:", err))
			}
			encoded2 = dst.Bytes()
			if !bytes.Equal(encoded, encoded2) {
				got, err = dec.DecodeAll(encoded2, got[:0])
				if err != nil {
					t.Fatal(fmt.Sprintln("Dict Level", level, "DecodeAll (buffer) error:", err, "\norg:", len(data), "\nencoded", len(encoded2)))
				}
				if !bytes.Equal(got, data) {
					t.Fatal(fmt.Sprintln("Dict Level", level, "DecodeAll (buffer) output mismatch\n", len(got), "org: \n", len(data), "(want)", "\nencoded:", len(encoded2)))
				}
			}
		}
	})
}
