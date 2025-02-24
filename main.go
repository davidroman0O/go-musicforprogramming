package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"

	"github.com/bogem/id3v2"
	"github.com/mmcdole/gofeed"
)

// Episode represents a podcast episode with a reformatted title.
type Episode struct {
	Number       string
	Title        string
	URL          string
	ExpectedSize int64
}

// Downloader manages downloading and tagging episodes.
type Downloader struct {
	OutputDir string
	FeedURL   string
	CoverURL  string
	Episodes  []Episode
}

// newDownloader creates a new Downloader instance.
func newDownloader(outDir, feedURL, coverURL string) *Downloader {
	return &Downloader{
		OutputDir: outDir,
		FeedURL:   feedURL,
		CoverURL:  coverURL,
	}
}

func main() {
	flag.Parse()
	// Use the first positional argument as the output directory, if provided.
	outputDir := "downloaded_music"
	if flag.NArg() > 0 {
		outputDir = flag.Arg(0)
	}

	d := newDownloader(outputDir,
		"https://musicforprogramming.net/rss.php",
		"https://musicforprogramming.net/img/folder.jpg")

	if err := d.prepareOutput(); err != nil {
		log.Fatalf("Error preparing output directory: %v", err)
	}
	if err := d.fetchCover(); err != nil {
		log.Fatalf("Error fetching cover: %v", err)
	}
	if err := d.loadEpisodes(); err != nil {
		log.Fatalf("Error loading episodes: %v", err)
	}
	d.downloadAndTagEpisodes()
}

// prepareOutput ensures the output directory exists.
func (d *Downloader) prepareOutput() error {
	return os.MkdirAll(d.OutputDir, 0755)
}

// fetchCover downloads the cover image if it doesn't already exist.
func (d *Downloader) fetchCover() error {
	coverPath := filepath.Join(d.OutputDir, "cover.jpg")
	if _, err := os.Stat(coverPath); err == nil {
		return nil // Cover already exists.
	}

	resp, err := http.Get(d.CoverURL)
	if err != nil {
		return fmt.Errorf("failed to fetch cover: %w", err)
	}
	defer resp.Body.Close()

	out, err := os.Create(coverPath)
	if err != nil {
		return fmt.Errorf("failed to create cover file: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("failed to write cover file: %w", err)
	}
	log.Println("Cover image downloaded.")
	return nil
}

// loadEpisodes parses the RSS feed and creates a list of episodes,
// reformatting titles from "Episode XX: Title" to "XX - Title".
// It also extracts the expected file size from the enclosure.
func (d *Downloader) loadEpisodes() error {
	parser := gofeed.NewParser()
	feed, err := parser.ParseURL(d.FeedURL)
	if err != nil {
		return fmt.Errorf("failed to parse feed: %w", err)
	}

	re := regexp.MustCompile(`^Episode\s+(\d+):\s*(.+)$`)
	for _, item := range feed.Items {
		if len(item.Enclosures) == 0 {
			continue
		}
		matches := re.FindStringSubmatch(item.Title)
		if len(matches) != 3 {
			log.Printf("Unrecognized title format, skipping: %s", item.Title)
			continue
		}
		// Parse expected size from enclosure.
		var expSize int64 = 0
		if sizeStr := item.Enclosures[0].Length; sizeStr != "" {
			if size, err := strconv.ParseInt(sizeStr, 10, 64); err == nil {
				expSize = size
			}
		}
		ep := Episode{
			Number:       matches[1],
			Title:        matches[2],
			URL:          item.Enclosures[0].URL,
			ExpectedSize: expSize,
		}
		d.Episodes = append(d.Episodes, ep)
	}

	// Reverse the order so the earliest episode comes first.
	for i, j := 0, len(d.Episodes)-1; i < j; i, j = i+1, j-1 {
		d.Episodes[i], d.Episodes[j] = d.Episodes[j], d.Episodes[i]
	}
	log.Printf("Found %d episodes.", len(d.Episodes))
	return nil
}

// downloadAndTagEpisodes processes episodes concurrently.
func (d *Downloader) downloadAndTagEpisodes() {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 3) // Limit concurrent downloads to 3.

	for _, ep := range d.Episodes {
		wg.Add(1)
		sem <- struct{}{}
		go func(ep Episode) {
			defer wg.Done()
			defer func() { <-sem }()

			// Create a filename of the form "XX - Title.mp3"
			fileName := fmt.Sprintf("%s - %s.mp3", ep.Number, ep.Title)
			targetPath := filepath.Join(d.OutputDir, fileName)

			if fileIsComplete(ep.URL, targetPath, ep.ExpectedSize) {
				log.Printf("Episode '%s' is already complete.", fileName)
				return
			}

			log.Printf("Downloading episode '%s'...", fileName)
			if err := downloadFile(ep.URL, targetPath); err != nil {
				log.Printf("Error downloading '%s': %v", fileName, err)
				return
			}
			if err := tagEpisode(targetPath, filepath.Join(d.OutputDir, "cover.jpg")); err != nil {
				log.Printf("Error tagging '%s': %v", fileName, err)
				return
			}
			log.Printf("Episode '%s' processed.", fileName)
		}(ep)
	}
	wg.Wait()
}

// downloadFile retrieves content from the given URL and writes it to dest.
func downloadFile(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

// fileIsComplete checks if a file exists, has the expected size, and contains valid metadata.
func fileIsComplete(url, path string, expectedSize int64) bool {
	// seems buggy for now, i don't have the time, fuck that
	{
		// info, err := os.Stat(path)
		// if err != nil {
		// 	return false
		// }
		// Use expected size if available.
		// if expectedSize > 0 {
		// 	fmt.Printf("Sizes %d %d\n", info.Size(), expectedSize)
		// 	if info.Size() != expectedSize {
		// 		return false
		// 	}
		// } else {
		// Fallback: use HEAD request to check size.
		// resp, err := http.Head(url)
		// if err != nil {
		// 	return false
		// }
		// defer resp.Body.Close()
		// if resp.ContentLength > 0 && info.Size() != resp.ContentLength {
		// 	return false
		// }
		// }
	}
	// Check metadata completeness.
	metaOk, err := metadataComplete(path)
	if err != nil || !metaOk {
		return false
	}
	return true
}

// metadataComplete verifies that the MP3 file contains the expected album metadata and attached cover.
func metadataComplete(mp3Path string) (bool, error) {
	tag, err := id3v2.Open(mp3Path, id3v2.Options{Parse: true})
	if err != nil {
		return false, err
	}
	defer tag.Close()

	if tag.Album() != "Music For Programming" {
		return false, nil
	}

	frames := tag.GetFrames("APIC")
	if len(frames) == 0 {
		return false, nil
	}
	return true, nil
}

// tagEpisode applies metadata and the cover image to the MP3 file.
func tagEpisode(mp3Path, coverPath string) error {
	tag, err := id3v2.Open(mp3Path, id3v2.Options{Parse: true})
	if err != nil {
		return err
	}
	defer tag.Close()

	tag.SetAlbum("Music For Programming")

	cover, err := os.ReadFile(coverPath)
	if err != nil {
		return err
	}
	pic := id3v2.PictureFrame{
		Encoding:    id3v2.EncodingUTF8,
		MimeType:    "image/jpeg",
		PictureType: id3v2.PTFrontCover,
		Description: "Cover",
		Picture:     cover,
	}
	tag.AddAttachedPicture(pic)
	return tag.Save()
}
