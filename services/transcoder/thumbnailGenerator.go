package transcoder

import (
	"os"
	"os/exec"
	"path"
	"strconv"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/config"
	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/utils"
)

// ThumbnailGenerator periodically snapshots the most recent HLS segment
// into thumbnail.jpg + preview.gif while a stream is online. One
// instance per active stream, owned by the stream service.
type ThumbnailGenerator struct {
	// mu guards timer and done: Start runs on the stream's goroutine while Stop
	// can be called from the lifecycle goroutine.
	mu               sync.Mutex
	timer            *time.Ticker
	done             chan struct{}
	cfg              *config.Config
	configRepository models.EngineConfig
}

// NewThumbnailGenerator returns an idle generator. Call Start to begin.
func NewThumbnailGenerator(cfg *config.Config, configRepository models.EngineConfig) *ThumbnailGenerator {
	return &ThumbnailGenerator{cfg: cfg, configRepository: configRepository}
}

// Stop will stop the periodic generating of a thumbnail from video.
func (g *ThumbnailGenerator) Stop() {
	g.mu.Lock()
	timer := g.timer
	done := g.done
	g.timer = nil
	g.done = nil
	g.mu.Unlock()

	// A ticker's Stop does not close its channel, so the generator goroutine used
	// to block in "for range g.timer.C" forever: one goroutine and one ticker
	// leaked for every stream that ended.
	if done != nil {
		close(done)
	}

	if timer != nil {
		timer.Stop()
	}
}

// Start starts generating thumbnails.
func (g *ThumbnailGenerator) Start(chunkPath string, variantIndex int, isVideoPassthrough bool) {
	// Every 20 seconds create a thumbnail from the most
	// recent video segment.
	timer := time.NewTicker(20 * time.Second)
	done := make(chan struct{})

	g.mu.Lock()
	g.timer = timer
	g.done = done
	g.mu.Unlock()

	go func() {
		defer log.Debug("thumbnail generator has stopped")

		failures := 0

		for {
			select {
			case <-done:
				return
			case <-timer.C:
				if err := g.fireThumbnailGenerator(chunkPath, variantIndex); err != nil {
					failures++

					// Report the first failure, then keep retrying quietly: a variant
					// directory that is missing produced one error every 20 seconds for the
					// whole stream (525 lines in a three hour run) without ever recovering,
					// while a transient failure still needs to be visible.
					logMsg := err.Error()
					if isVideoPassthrough {
						logMsg += ". Video Passthrough is enabled. You should disable it to fix this, and other, streaming errors. https://owncast.online/troubleshoot"
					}

					if failures == 1 {
						log.Errorln("Unable to generate thumbnail:", logMsg)
					} else {
						log.Debugln("Unable to generate thumbnail:", logMsg)
					}
				} else if failures != 0 {
					failures = 0
				}
			}
		}
	}()
}

func (g *ThumbnailGenerator) fireThumbnailGenerator(segmentPath string, variantIndex int) error {
	// JPG takes less time to encode than PNG
	outputFile := path.Join(g.cfg.TempDir, "thumbnail.jpg")
	previewGifFile := path.Join(g.cfg.TempDir, "preview.gif")

	framePath := path.Join(segmentPath, strconv.Itoa(variantIndex))
	files, err := os.ReadDir(framePath)
	if err != nil {
		return err
	}

	var modTime time.Time
	var names []string
	for _, f := range files {
		if path.Ext(f.Name()) != ".ts" {
			continue
		}

		fi, err := f.Info()
		if err != nil {
			continue
		}

		if fi.Mode().IsRegular() {
			if !fi.ModTime().Before(modTime) {
				if fi.ModTime().After(modTime) {
					modTime = fi.ModTime()
					names = names[:0]
				}
				names = append(names, fi.Name())
			}
		}
	}

	if len(names) == 0 {
		return nil
	}
	mostRecentFile := path.Join(framePath, names[0])
	ffmpegPath := utils.ValidatedFfmpegPath(g.configRepository.GetFfMpegPath())
	outputFileTemp := path.Join(g.cfg.TempDir, "tempthumbnail.jpg")

	thumbnailCmdFlags := thumbnailCommandFlags(mostRecentFile, outputFileTemp, g.configRepository.GetVideoCodec())

	if _, err := exec.Command(ffmpegPath, thumbnailCmdFlags...).Output(); err != nil {
		return err
	}

	// rename temp file
	if err := utils.Move(outputFileTemp, outputFile); err != nil {
		log.Errorln(err)
	}

	g.makeAnimatedGifPreview(mostRecentFile, previewGifFile)

	return nil
}

// thumbnailDecodeFlags returns input decode-acceleration flags for the
// configured global video codec. JPEG and GIF are image encoders with no
// hardware counterpart, so the configured encoder cannot encode the preview;
// its hardware decoder can still be used for the segment decode to keep the
// periodic preview work off the CPU. Only NVENC exposes a decode-safe flag
// (plain -hwaccel cuda); VAAPI/QSV flag sets need explicit hwdownload filters
// before image encoders, so they intentionally keep the software path.
func thumbnailDecodeFlags(codecName string) []string {
	// Decode acceleration is codec-defined so a hardware encoder's flags can
	// never leak into a software encoder's argv (or vice versa).
	return getCodec(codecName).DecodeFlags()
}

// thumbnailCommandFlags builds the still-frame extraction command. Kept pure so
// the ordering of decode flags before the input is unit tested.
func thumbnailCommandFlags(input, output, codecName string) []string {
	flags := make([]string, 0, 12)
	flags = append(
		flags,
		"-y",            // Overwrite file
		"-threads", "1", // Low priority processing
	)
	flags = append(flags, thumbnailDecodeFlags(codecName)...)
	flags = append(
		flags,
		"-t", "1", // Pull from frame 1
		"-i", input, // Input
		"-f", "image2", // format
		"-vframes", "1", // Single frame
		output,
	)

	return flags
}

// animatedGifCommandFlags builds the animated preview command, using the same
// hardware decode acceleration as the still thumbnail.
func animatedGifCommandFlags(input, output, codecName string) []string {
	flags := make([]string, 0, 12)
	flags = append(
		flags,
		"-y",            // Overwrite file
		"-threads", "1", // Low priority processing
	)
	flags = append(flags, thumbnailDecodeFlags(codecName)...)
	flags = append(
		flags,
		"-i", input, // Input
		"-t", "1", // Output is one second in length
		// Filter is pulled from https://engineering.giphy.com/how-to-make-gifs-with-ffmpeg/
		"-filter_complex", "[0:v] fps=8,scale=w=480:h=-1:flags=lanczos,split [a][b];[a] palettegen=stats_mode=full [p];[b][p] paletteuse=new=1",
		output,
	)

	return flags
}

func (g *ThumbnailGenerator) makeAnimatedGifPreview(sourceFile string, outputFile string) {
	ffmpegPath := utils.ValidatedFfmpegPath(g.configRepository.GetFfMpegPath())
	outputFileTemp := path.Join(g.cfg.TempDir, "temppreview.gif")

	animatedGifFlags := animatedGifCommandFlags(sourceFile, outputFileTemp, g.configRepository.GetVideoCodec())

	if _, err := exec.Command(ffmpegPath, animatedGifFlags...).Output(); err != nil {
		log.Errorln(err)
		// rename temp file
	} else if err := utils.Move(outputFileTemp, outputFile); err != nil {
		log.Errorln(err)
	}
}
