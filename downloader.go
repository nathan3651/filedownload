package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/k0kubun/go-ansi"
	"github.com/schollz/progressbar/v3"
)

// const flags = os.O_CREATE | os.O_WRONLY

type Downloader struct {
	concurreny int
	resume     bool

	bar *progressbar.ProgressBar
}

func NewDownloader(concurrency int, resume bool) *Downloader {
	return &Downloader{concurreny: concurrency, resume: resume}
}

// 通过Head请求，判断是否支持部分请求，如果支持，就直接下载整个文件
// 支持部分请求时ContentLengt可获取文件大小，有了文件大小和并发数，可计算出部分请求大大小
func (d *Downloader) Download(strURL, fileName string) error {
	if fileName == "" {
		fileName = path.Base(strURL)
	}

	resp, err := http.Head(strURL)
	if err != nil {
		return err
	}

	if resp.StatusCode == http.StatusOK && resp.Header.Get("Accept-Ranges") == "bytes" {
		return d.multiDownload(strURL, fileName, int(resp.ContentLength))
	}

	return d.singleDownload(strURL, fileName)
}

func (d *Downloader) multiDownload(strURL, fileName string, contentLen int) error {
	d.setBar(contentLen)

	partSize := contentLen / d.concurreny

	// 创建部分文件的存放目录
	partDir := d.getPartDir(fileName)
	os.Mkdir(partDir, 0777)
	defer os.RemoveAll(partDir)

	var wg sync.WaitGroup
	wg.Add(d.concurreny)
	rangeStart := 0

	for i := 0; i < d.concurreny; i++ {
		// 并发请求
		go func(i, rangeStart int) {
			defer wg.Done()
			rangeEnd := rangeStart + partSize
			//最后一部分，总长度不能超过contentLen
			if i == d.concurreny-1 {
				rangeEnd = contentLen
			}

			downloaded := 0
			if d.resume {
				partFilename := d.getPartFilename(fileName, i)
				content, err := ioutil.ReadFile(partFilename)
				if err != nil {
					downloaded = len(content)
				}
				d.bar.Add(downloaded)
			}
			d.downloadPartial(strURL, fileName, rangeStart+downloaded, rangeEnd, i)
		}(i, rangeStart)

		rangeStart += partSize + 1
	}

	wg.Wait()

	d.merge(fileName)

	return nil
}
func (d *Downloader) singleDownload(strURL, fileName string) error {
	resp, err := http.Get(strURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	d.setBar(int(resp.ContentLength))

	file, err := os.OpenFile(fileName, os.O_CREATE|os.O_WRONLY, 0666)
	if err != nil {
		return err
	}
	defer file.Close()

	buf := make([]byte, 32*1024)
	_, err = io.CopyBuffer(io.MultiWriter(file, d.bar), resp.Body, buf)
	if err != nil {
		return err
	}

	return nil
}

// 合并文件
func (d *Downloader) merge(fileName string) error {
	flags := os.O_CREATE | os.O_WRONLY
	destFile, err := os.OpenFile(fileName, flags, 0666)
	if err != nil {
		fmt.Println(err)
		return err
	}
	defer destFile.Close()

	for i := 0; i < d.concurreny; i++ {
		partFilename := d.getPartFilename(fileName, i)
		partFile, err := os.Open(partFilename)
		if err != nil {
			return err
		}
		io.Copy(destFile, partFile)
		partFile.Close()
		os.Remove(partFilename) //
	}

	return nil
}

// 发起range请求，将请求内容写入本地文件中红
func (d *Downloader) downloadPartial(strURL, fileName string, rangeStart, rangeEnd, i int) {
	if rangeStart >= rangeEnd {
		return
	}

	req, err := http.NewRequest("GET", strURL, nil)
	if err != nil {
		log.Fatal(err)
	}

	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", rangeStart, rangeEnd))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()

	flags := os.O_CREATE | os.O_WRONLY
	if d.resume {
		flags |= os.O_APPEND
	}

	partFile, err := os.OpenFile(d.getPartFilename(fileName, i), flags, 0666)
	if err != nil {
		log.Fatal(err)
	}
	defer partFile.Close()

	buf := make([]byte, 32*1024)
	_, err = io.CopyBuffer(io.MultiWriter(partFile, d.bar), resp.Body, buf)
	// _, err = io.CopyBuffer(partFile, resp.Body, buf)
	if err != nil {
		if err == io.EOF { //读到最后了
			return
		}
		log.Fatal(err)
	}
}

// getPartDir 部分文件存放的目录
func (d *Downloader) getPartDir(fileName string) string {
	return strings.SplitN(fileName, ".", 2)[0]
}

// getPartFilename 构造部分文件的名字
func (d *Downloader) getPartFilename(fileName string, partNum int) string {
	partDir := d.getPartDir(fileName)
	return fmt.Sprintf("%s/%s-%d", partDir, fileName, partNum)
}

func (d *Downloader) setBar(length int) {
	d.bar = progressbar.NewOptions(
		length,
		progressbar.OptionSetWriter(ansi.NewAnsiStdout()),
		progressbar.OptionEnableColorCodes(true),
		progressbar.OptionShowBytes(true),
		progressbar.OptionSetWidth(50),
		progressbar.OptionSetDescription("downloading..."),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "[green]=[reset]",
			SaucerHead:    "[green]>[reset]",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
	)
}
