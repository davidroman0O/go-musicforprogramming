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
	"sync"

	"github.com/bogem/id3v2"
	"github.com/mmcdole/gofeed"
)

// Episode represents a podcast episode with a reformatted title.
type Episode struct {
	Number string
	Title  string
	URL    string
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
		ep := Episode{
			Number: matches[1],
			Title:  matches[2],
			URL:    item.Enclosures[0].URL,
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

			if fileComplete(ep.URL, targetPath) {
				log.Printf("Episode '%s' already downloaded.", fileName)
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

// fileComplete checks if a file exists and its size matches the expected content length.
func fileComplete(url, path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	resp, err := http.Head(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return info.Size() == resp.ContentLength
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
