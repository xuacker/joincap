package main

import (
	"bufio"
	"container/heap"
	"fmt"
	"io"
	// "log"
	// "net/http"
	// _ "net/http/pprof"
	"os"

	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcapgo"
	"github.com/jessevdk/go-flags"
)

var opts struct {
	Verbose        bool   `short:"v" long:"verbose" description:"Explain when skipping packets or entire input files."`
	Version        bool   `short:"V" long:"version" description:"Print the version and exit."`
	OutputFilePath string `short:"w" default:"-" description:"Sets the output filename. If the name is '-', stdout will be used."`
	Rest           struct {
		InFiles []string
	} `positional-args:"yes" required:"yes"`
}

func max(x, y uint32) uint32 {
	if x > y {
		return x
	}
	return y
}

const version = "0.8.0"

func main() {
	// go func() {
	// 	log.Println(http.ListenAndServe("localhost:8080", nil))
	// }()

	_, err := flags.ParseArgs(&opts, os.Args)

	if err != nil {
		if flagsErr, ok := err.(*flags.Error); ok && flagsErr.Type == flags.ErrHelp {
			// -h flasg, print version and help and exit
			fmt.Println("joincap v" + version)
			os.Exit(0)
		} else {
			panic(err)
		}
	}

	if opts.Version {
		// -v flag, print version and exit
		fmt.Println("joincap v" + version)
		os.Exit(0)
	}
	if opts.Verbose {
		fmt.Fprintln(os.Stderr, "joincap v"+version)
	}

	minTimeHeap := PacketHeap{}
	heap.Init(&minTimeHeap)

	outputFile := os.Stdout
	if opts.OutputFilePath != "-" {
		outputFile, err = os.Create(opts.OutputFilePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, opts.OutputFilePath+":")
			panic(err)
		}
		defer outputFile.Close()
	}
	bufferedFileWriter := bufio.NewWriter(outputFile)
	defer bufferedFileWriter.Flush()

	writer := pcapgo.NewWriter(bufferedFileWriter)

	var totalInputSizeBytes int64
	var snaplen uint32
	var linkType layers.LinkType
	for _, inputPcapPath := range opts.Rest.InFiles[1:] {
		inputFile, err := os.Open(inputPcapPath)
		if err != nil {
			if opts.Verbose {
				fmt.Fprintln(os.Stderr, inputFile.Name()+":", err, "(skipping this file)")
			}
			continue
		}

		reader, err := pcapgo.NewReader(inputFile)
		if err != nil {
			if opts.Verbose {
				fmt.Fprintln(os.Stderr, inputFile.Name()+":", err, "(skipping this file)")
			}
			continue
		}

		fStat, _ := inputFile.Stat()
		totalInputSizeBytes += fStat.Size()

		snaplen = max(snaplen, reader.Snaplen())
		if linkType == layers.LinkTypeNull {
			linkType = reader.LinkType()
		} else if linkType != reader.LinkType() {
			panic(fmt.Sprintln(inputFile.Name()+":", "Different LinkTypes:", linkType, reader.LinkType()))
		}

		nextPacket, err := readNext(reader, inputFile)
		if err == nil {
			heap.Push(&minTimeHeap, nextPacket)
		}
	}

	if opts.Verbose {
		fmt.Fprintf(os.Stderr, "merging %d input files of size %f GiB\n", minTimeHeap.Len(), float64(totalInputSizeBytes)/1024/1024/1024)
		fmt.Fprintf(os.Stderr, "writing to %s\n", outputFile.Name())
	}

	writer.WriteFileHeader(snaplen, linkType)
	for minTimeHeap.Len() > 0 {
		// find the earliest packet and write it to the output file
		packet := heap.Pop(&minTimeHeap).(Packet)
		write(writer, packet)

		var earliestHeapTime int64
		if minTimeHeap.Len() > 0 {
			earliestHeapTime = minTimeHeap[0].CaptureInfo.Timestamp.UnixNano()
		}
		for {
			// read the next packet from the source of the last written packet
			nextPacket, err := readNext(packet.Reader, packet.InputFile)
			if err == io.EOF {
				break
			}

			if nextPacket.CaptureInfo.Timestamp.UnixNano() <= earliestHeapTime {
				// this is the earliest packet, write it to the output file
				write(writer, nextPacket)
				continue
			}

			// this is not the earliest packet, push it to the heap for sorting
			heap.Push(&minTimeHeap, nextPacket)
			break
		}
	}
}

func readNext(reader *pcapgo.Reader, inputFile *os.File) (Packet, error) {
	for {
		data, captureInfo, err := reader.ReadPacketData()
		if err != nil {
			if err == io.EOF {
				if opts.Verbose {
					fmt.Fprintln(os.Stderr, inputFile.Name()+": done")
				}
				inputFile.Close()

				return Packet{}, err
			}
			if opts.Verbose {
				fmt.Fprintln(os.Stderr, inputFile.Name()+":", err, "(skipping this packet)")
			}
			// skip errors
			continue
		}

		return Packet{
			CaptureInfo: captureInfo,
			Data:        data,
			Reader:      reader,
			InputFile:   inputFile}, nil
	}
}

func write(writer *pcapgo.Writer, packet Packet) {
	err := writer.WritePacket(packet.CaptureInfo, packet.Data)
	if err != nil && opts.Verbose {
		fmt.Fprintln(os.Stderr, err, "(skipping this packet)")
		// skip errors
	}
}
