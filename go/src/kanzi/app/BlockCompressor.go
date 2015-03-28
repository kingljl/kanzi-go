/*
Copyright 2011-2013 Frederic Langlet
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
you may obtain a copy of the License at

                http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"flag"
	"fmt"
	"kanzi"
	"kanzi/io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	COMP_DEFAULT_BUFFER_SIZE = 32768
	WARN_EMPTY_INPUT         = -128
)

type BlockCompressor struct {
	verbosity    uint
	overwrite    bool
	checksum     bool
	inputName    string
	outputName   string
	entropyCodec string
	transform    string
	blockSize    uint
	jobs         uint
	listeners    []io.BlockListener
}

func NewBlockCompressor() (*BlockCompressor, error) {
	this := new(BlockCompressor)

	// Define flags
	var help = flag.Bool("help", false, "display the help message")
	var verbose = flag.Int("verbose", 1, "set the verbosity level [0..4]")
	var overwrite = flag.Bool("overwrite", false, "overwrite the output file if it already exists")
	var inputName = flag.String("input", "", "mandatory name of the input file to encode")
	var outputName = flag.String("output", "", "optional name of the output file (defaults to <input.knz>), or 'none' for dry-run")
	var blockSize = flag.String("block", "1048576", "size of the input blocks, multiple of 8, max 512 MB (depends on transform), min 1KB, default 1MB")
	var entropy = flag.String("entropy", "Huffman", "entropy codec to use [None|Huffman*|ANS|Range|PAQ|FPAQ|CM]")
	var function = flag.String("transform", "BWT+MTF", "transform to use [None|BWT|BWTS|Snappy|LZ4|RLT]")
	var cksum = flag.Bool("checksum", false, "enable block checksum")
	var tasks = flag.Int("jobs", 1, "number of concurrent jobs")

	// Parse
	flag.Parse()

	if *help == true {
		printOut("-help                : display this message", true)
		printOut("-verbose=<level>     : set the verbosity level", true)
		printOut("                       0:silent, 1:default, 2:display block size (byte rounded), 3:display timings, 4:display extra information", true)
		printOut("-overwrite           : overwrite the output file if it already exists", true)
		printOut("-input=<inputName>   : mandatory name of the input file to encode", true)
		printOut("-output=<outputName> : optional name of the output file (defaults to <input.knz>) or 'none' for dry-run", true)
		printOut("-block=<size>        : size of the input blocks, multiple of 8, max 512 MB (depends on transform), min 1KB, default 1MB", true)
		printOut("-entropy=<codec>     : entropy codec to use [None|Huffman*|ANS|Range|PAQ|FPAQ|CM]", true)
		printOut("-transform=<codec>   : transform to use [None|BWT*|BWTS|Snappy|LZ4|RLT]", true)
		printOut("                       for BWT(S), an optional GST can be provided: [MTF|RANK|TIMESTAMP]", true)
		printOut("                       EG: BWT+RANK or BWTS+MTF (default is BWT+MTF)", true)
		printOut("-checksum            : enable block checksum", true)
		printOut("-jobs=<jobs>         : number of concurrent jobs", true)
		printOut("", true)
		printOut("EG. go run BlockCompressor -input=foo.txt -output=foo.knz -overwrite -transform=BWT+MTF -block=4m -entropy=FPAQ -verbose -jobs=4", true)
		os.Exit(0)
	}

	if len(*inputName) == 0 {
		fmt.Printf("Missing input file name, exiting ...\n")
		os.Exit(io.ERR_MISSING_PARAM)
	}

	if len(*outputName) == 0 {
		*outputName = *inputName + ".knz"
	}

	if *tasks < 1 {
		fmt.Printf("Invalid number of jobs provided on command line: %v\n", *tasks)
		os.Exit(io.ERR_INVALID_PARAM)
	}

	if *verbose < 0 {
		fmt.Printf("Invalid verbosity level provided on command line: %v\n", *verbose)
		os.Exit(io.ERR_INVALID_PARAM)
	}

	this.verbosity = uint(*verbose)
	this.overwrite = *overwrite
	this.inputName = *inputName
	this.outputName = *outputName
	strBlockSize := strings.ToUpper(*blockSize)

	// Process K or M suffix
	scale := 1

	if strBlockSize[len(strBlockSize)-1] == 'K' {
		strBlockSize = strBlockSize[0 : len(strBlockSize)-1]
		scale = 1024
	} else if strBlockSize[len(strBlockSize)-1] == 'M' {
		strBlockSize = strBlockSize[0 : len(strBlockSize)-1]
		scale = 1024 * 1024
	}

	bSize, err := strconv.Atoi(strBlockSize)

	if err != nil || bSize <= 0 {
		fmt.Printf("Invalid block size provided on command line: %v\n", *blockSize)
		os.Exit(io.ERR_BLOCK_SIZE)
	}

	this.blockSize = uint(scale * bSize)
	this.entropyCodec = strings.ToUpper(*entropy)
	this.transform = strings.ToUpper(*function)
	this.checksum = *cksum
	this.jobs = uint(*tasks)
	this.listeners = make([]io.BlockListener, 0)

	if this.verbosity > 1 {
		if listener, err := io.NewInfoPrinter(this.verbosity, io.ENCODING, os.Stdout); err == nil {
			this.AddListener(listener)
		}
	}

	return this, nil
}

func (this *BlockCompressor) AddListener(bl io.BlockListener) bool {
	if bl == nil {
		return false
	}

	this.listeners = append(this.listeners, bl)
	return true
}

func (this *BlockCompressor) RemoveListener(bl io.BlockListener) bool {
	for i, e := range this.listeners {
		if e == bl {
			this.listeners = append(this.listeners[:i-1], this.listeners[i+1:]...)
			return true
		}
	}

	return false
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	bc, err := NewBlockCompressor()

	if err != nil {
		fmt.Printf("Failed to create block compressor: %v\n", err)
		os.Exit(io.ERR_CREATE_COMPRESSOR)
	}

	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("An unexpected error occured during compression: %v\n", r.(error))
			os.Exit(io.ERR_UNKNOWN)
		}
	}()

	code, _ := bc.call()
	os.Exit(code)
}

// Return exit code, number of bits written
func (this *BlockCompressor) call() (int, uint64) {
	var msg string
	printFlag := this.verbosity > 1
	printOut("Input file name set to '"+this.inputName+"'", printFlag)
	printOut("Output file name set to '"+this.outputName+"'", printFlag)
	msg = fmt.Sprintf("Block size set to %d bytes", this.blockSize)
	printOut(msg, printFlag)
	msg = fmt.Sprintf("Verbose set to %t", printFlag)
	printOut(msg, printFlag)
	msg = fmt.Sprintf("Overwrite set to %t", this.overwrite)
	printOut(msg, printFlag)
	msg = fmt.Sprintf("Checksum set to %t", this.checksum)
	printOut(msg, printFlag)
	w1 := "no"

	if this.transform != "NONE" {
		w1 = this.transform
	}

	msg = fmt.Sprintf("Using %s transform (stage 1)", w1)
	printOut(msg, printFlag)
	w2 := "no"

	if this.entropyCodec != "NONE" {
		w2 = this.entropyCodec
	}

	msg = fmt.Sprintf("Using %s entropy codec (stage 2)", w2)
	printOut(msg, printFlag)
	prefix := ""

	if this.jobs > 1 {
		prefix = "s"
	}

	msg = fmt.Sprintf("Using %d job%s", this.jobs, prefix)
	printOut(msg, printFlag)
	written := uint64(0)
	var output *os.File
	var bos kanzi.OutputStream

	if strings.ToUpper(this.outputName) != "NONE" {
		var err error
		output, err = os.OpenFile(this.outputName, os.O_RDWR, 666)

		if err == nil {
			// File exists
			output.Close()

			if this.overwrite == false {
				fmt.Print("The output file exists and the 'overwrite' command ")
				fmt.Println("line option has not been provided")
				return io.ERR_OVERWRITE_FILE, written
			}
		}

		output, err = os.Create(this.outputName)

		if err != nil {
			fmt.Printf("Cannot open output file '%v' for writing: %v\n", this.outputName, err)
			return io.ERR_CREATE_FILE, written
		}

		defer output.Close()

		bos, err = io.NewBufferedOutputStream(output)

		if err != nil {
			fmt.Printf("Cannot create compressed stream: %s\n", err.Error())
			return io.ERR_CREATE_COMPRESSOR, written
		}
	} else {
		bos, _ = io.NewNullOutputStream()
	}

	verboseWriter := os.Stdout

	if printFlag == false {
		verboseWriter = nil
	}

	cos, err := io.NewCompressedOutputStream(this.entropyCodec, this.transform,
		bos, this.blockSize, this.checksum, verboseWriter, this.jobs)

	if err != nil {
		if ioerr, isIOErr := err.(io.IOError); isIOErr == true {
			fmt.Printf("%s\n", ioerr.Error())
			return ioerr.ErrorCode(), written
		} else {
			fmt.Printf("Cannot create compressed stream: %s\n", err.Error())
			return io.ERR_CREATE_COMPRESSOR, written
		}
	}

	defer cos.Close()
	input, err := os.Open(this.inputName)

	if err != nil {
		fmt.Printf("Cannot open input file '%v': %v\n", this.inputName, err)
		return io.ERR_OPEN_FILE, written
	}

	defer input.Close()

	for _, bl:= range this.listeners {
		cos.AddListener(bl)
	}

	// Encode
	len := 0
	read := int64(0)
	silent := this.verbosity < 1
	printOut("Encoding ...", !silent)
	written = cos.GetWritten()
	buffer := make([]byte, COMP_DEFAULT_BUFFER_SIZE)
	before := time.Now()
	len, err = input.Read(buffer)

	for len > 0 {
		if err != nil {
			fmt.Printf("Failed to read block from file '%v': %v\n", this.inputName, err)
			return io.ERR_READ_FILE, written
		}

		read += int64(len)

		if _, err = cos.Write(buffer[0:len]); err != nil {
			if ioerr, isIOErr := err.(io.IOError); isIOErr == true {
				fmt.Printf("%s\n", ioerr.Error())
				return ioerr.ErrorCode(), written
			} else {
				fmt.Printf("An unexpected condition happened. Exiting ...\n%v\n", err.Error())
				return io.ERR_PROCESS_BLOCK, written
			}
		}

		len, err = input.Read(buffer)
	}

	if read == 0 {
		fmt.Println("Empty input file ... nothing to do")
		return WARN_EMPTY_INPUT, written
	}

	// Close streams to ensure all data are flushed
	// Deferred close is fallback for error paths
	if err := cos.Close(); err != nil {
		fmt.Printf("%v\n", err)
		return io.ERR_PROCESS_BLOCK, written
	}

	after := time.Now()
	delta := after.Sub(before).Nanoseconds() / 1000000 // convert to ms

	printOut("", !silent)
	msg = fmt.Sprintf("Encoding:          %d ms", delta)
	printOut(msg, !silent)
	msg = fmt.Sprintf("Input size:        %d", read)
	printOut(msg, !silent)
	msg = fmt.Sprintf("Output size:       %d", cos.GetWritten())
	printOut(msg, !silent)
	msg = fmt.Sprintf("Ratio:             %f", float64(cos.GetWritten())/float64(read))
	printOut(msg, !silent)

	if delta > 0 {
		msg = fmt.Sprintf("Throughput (KB/s): %d", ((read*int64(1000))>>10)/delta)
		printOut(msg, !silent)
	}

	printOut("", !silent)
	return 0, cos.GetWritten()
}

func printOut(msg string, print bool) {
	if print == true {
		fmt.Println(msg)
	}
}
