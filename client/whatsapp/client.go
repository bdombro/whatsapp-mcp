package whatsapp

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"go.mau.fi/whatsmeow"
	//lint:ignore SA1019 whatsmeow/binary/proto is deprecated; keep aliases until migrated to waE2E
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// SendMessageResponse represents the response for the send message API.
type SendMessageResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// SendMessageRequest represents the request body for the send message API.
type SendMessageRequest struct {
	Recipient string `json:"recipient"`
	Message   string `json:"message"`
	MediaPath string `json:"media_path,omitempty"`
}

// sendWhatsAppMessage is the daemon-side live send path: it resolves `recipient`, optionally uploads `mediaPath`, then sends through the connected client.
func sendWhatsAppMessage(client *whatsmeow.Client, recipient string, message string, mediaPath string) (bool, string) {
	if !client.IsConnected() {
		return false, "Not connected to WhatsApp"
	}

	var recipientJID types.JID
	var err error

	isJID := strings.Contains(recipient, "@")

	if isJID {
		recipientJID, err = types.ParseJID(recipient)
		if err != nil {
			return false, fmt.Sprintf("Error parsing JID: %v", err)
		}
	} else {
		recipientJID = types.JID{
			User:   recipient,
			Server: "s.whatsapp.net",
		}
	}

	//lint:ignore SA1019 waProto.Message is a deprecated alias to waE2E.Message
	msg := &waProto.Message{}

	if mediaPath != "" {
		resolved, err := checkSendablePath(mediaPath)
		if err != nil {
			return false, err.Error()
		}
		mediaData, err := os.ReadFile(resolved)
		if err != nil {
			return false, fmt.Sprintf("Error reading media file: %v", err)
		}

		fileExt := strings.ToLower(mediaPath[strings.LastIndex(mediaPath, ".")+1:])
		var mediaType whatsmeow.MediaType
		var mimeType string

		switch fileExt {
		case "jpg", "jpeg":
			mediaType = whatsmeow.MediaImage
			mimeType = "image/jpeg"
		case "png":
			mediaType = whatsmeow.MediaImage
			mimeType = "image/png"
		case "gif":
			mediaType = whatsmeow.MediaImage
			mimeType = "image/gif"
		case "webp":
			mediaType = whatsmeow.MediaImage
			mimeType = "image/webp"
		case "ogg":
			mediaType = whatsmeow.MediaAudio
			mimeType = "audio/ogg; codecs=opus"
		case "mp4":
			mediaType = whatsmeow.MediaVideo
			mimeType = "video/mp4"
		case "avi":
			mediaType = whatsmeow.MediaVideo
			mimeType = "video/avi"
		case "mov":
			mediaType = whatsmeow.MediaVideo
			mimeType = "video/quicktime"
		default:
			mediaType = whatsmeow.MediaDocument
			mimeType = "application/octet-stream"
		}

		resp, err := client.Upload(context.Background(), mediaData, mediaType)
		if err != nil {
			return false, fmt.Sprintf("Error uploading media: %v", err)
		}

		fmt.Println("Media uploaded", resp)

		switch mediaType {
		case whatsmeow.MediaImage:
			//lint:ignore SA1019 waProto.ImageMessage is a deprecated alias
			msg.ImageMessage = &waProto.ImageMessage{
				Caption:       proto.String(message),
				Mimetype:      proto.String(mimeType),
				URL:           &resp.URL,
				DirectPath:    &resp.DirectPath,
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    &resp.FileLength,
			}
		case whatsmeow.MediaAudio:
			var seconds uint32 = 30
			var waveform []byte

			if strings.Contains(mimeType, "ogg") {
				analyzedSeconds, analyzedWaveform, err := analyzeOggOpus(mediaData)
				if err == nil {
					seconds = analyzedSeconds
					waveform = analyzedWaveform
				} else {
					return false, fmt.Sprintf("Failed to analyze Ogg Opus file: %v", err)
				}
			} else {
				fmt.Printf("Not an Ogg Opus file: %s\n", mimeType)
			}

			//lint:ignore SA1019 waProto.AudioMessage is a deprecated alias
			msg.AudioMessage = &waProto.AudioMessage{
				Mimetype:      proto.String(mimeType),
				URL:           &resp.URL,
				DirectPath:    &resp.DirectPath,
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    &resp.FileLength,
				Seconds:       proto.Uint32(seconds),
				PTT:           proto.Bool(true),
				Waveform:      waveform,
			}
		case whatsmeow.MediaVideo:
			//lint:ignore SA1019 waProto.VideoMessage is a deprecated alias
			msg.VideoMessage = &waProto.VideoMessage{
				Caption:       proto.String(message),
				Mimetype:      proto.String(mimeType),
				URL:           &resp.URL,
				DirectPath:    &resp.DirectPath,
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    &resp.FileLength,
			}
		case whatsmeow.MediaDocument:
			//lint:ignore SA1019 waProto.DocumentMessage is a deprecated alias
			msg.DocumentMessage = &waProto.DocumentMessage{
				Title:         proto.String(mediaPath[strings.LastIndex(mediaPath, "/")+1:]),
				Caption:       proto.String(message),
				Mimetype:      proto.String(mimeType),
				URL:           &resp.URL,
				DirectPath:    &resp.DirectPath,
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    &resp.FileLength,
			}
		}
	} else {
		msg.Conversation = proto.String(message)
	}

	_, err = client.SendMessage(context.Background(), recipientJID, msg)
	if err != nil {
		return false, fmt.Sprintf("Error sending message: %v", err)
	}

	return true, fmt.Sprintf("Message sent to %s", recipient)
}

// mediaDownloader implements the whatsmeow Downloadable interface.
type mediaDownloader struct {
	url           string
	directPath    string
	mediaKey      []byte
	fileLength    uint64
	fileSHA256    []byte
	fileEncSHA256 []byte
}

// GetDirectPath returns the WhatsApp direct path so whatsmeow can fetch the encrypted payload.
func (d *mediaDownloader) GetDirectPath() string { return d.directPath }

// GetURL returns the media URL so whatsmeow can fetch the encrypted payload.
func (d *mediaDownloader) GetURL() string { return d.url }

// GetMediaKey returns the media key used to decrypt the downloaded payload.
func (d *mediaDownloader) GetMediaKey() []byte { return d.mediaKey }

// GetFileLength returns the stored media length so whatsmeow can validate the download.
func (d *mediaDownloader) GetFileLength() uint64 { return d.fileLength }

// GetFileSHA256 returns the plaintext SHA so whatsmeow can validate the download.
func (d *mediaDownloader) GetFileSHA256() []byte { return d.fileSHA256 }

// GetFileEncSHA256 returns the encrypted SHA so whatsmeow can validate the download.
func (d *mediaDownloader) GetFileEncSHA256() []byte { return d.fileEncSHA256 }

// downloadMedia is the daemon-side live download path: it reconstructs media params from SQLite, fetches from WhatsApp, and writes under `dataDir/downloads`.
func downloadMedia(client *whatsmeow.Client, messageStore *MessageStore, messageID, chatJID string, dataDir string) (bool, string, string, string, error) {
	var mediaType, filename, url string
	var mediaKey, fileSHA256, fileEncSHA256 []byte
	var fileLength uint64

	err := messageStore.db.QueryRow(`
		SELECT media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length
		FROM messages 
		WHERE id = ? AND chat_jid = ?
	`, messageID, chatJID).Scan(&mediaType, &filename, &url, &mediaKey, &fileSHA256, &fileEncSHA256, &fileLength)

	if err != nil {
		return false, "", "", "", fmt.Errorf("failed to find message: %w", err)
	}

	if mediaType == "" || url == "" {
		return false, "", "", "", fmt.Errorf("message does not contain media")
	}

	downloader := &mediaDownloader{
		url:           url,
		directPath:    extractDirectPathFromURL(url),
		mediaKey:      mediaKey,
		fileSHA256:    fileSHA256,
		fileEncSHA256: fileEncSHA256,
		fileLength:    fileLength,
	}

	data, err := client.Download(context.Background(), downloader)
	if err != nil {
		return false, "", "", "", fmt.Errorf("failed to download media: %w", err)
	}

	downloadDir := filepath.Join(dataDir, "downloads")
	if err := os.MkdirAll(downloadDir, 0o700); err != nil {
		return false, "", "", "", fmt.Errorf("failed to create downloads dir: %w", err)
	}

	filePath := filepath.Join(downloadDir, downloadFileName(filename, messageID, mediaType))
	if filepath.Dir(filePath) != downloadDir { // nocov: downloadFileName never returns a separator
		return false, "", "", "", fmt.Errorf("refusing to write outside %s", downloadDir)
	}

	err = os.WriteFile(filePath, data, 0o600)
	if err != nil {
		return false, "", "", "", fmt.Errorf("failed to save media: %w", err)
	}

	return true, filePath, mediaType, filename, nil
}

// checkSendablePath refuses media paths that could leak secrets if a prompt-injected agent sends them: anything outside
// $HOME and the temp dirs, any hidden path component (~/.ssh, ~/.config, this plugin's data dir under ~/.local), and
// ~/Library. Symlinks are resolved first.
func checkSendablePath(mediaPath string) (string, error) {
	if !filepath.IsAbs(mediaPath) {
		return "", fmt.Errorf("media path must be absolute: %s", mediaPath)
	}
	resolved, err := filepath.EvalSymlinks(mediaPath)
	if err != nil {
		return "", fmt.Errorf("error reading media file: %v", err)
	}
	if info, err := os.Stat(resolved); err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("media path is not a regular file: %s", mediaPath)
	}
	home, _ := os.UserHomeDir()
	roots := []string{"/tmp", "/private/tmp", "/var/folders", "/private/var/folders", os.TempDir()}
	if home != "" {
		roots = append(roots, home)
		if realHome, err := filepath.EvalSymlinks(home); err == nil {
			roots = append(roots, realHome)
		}
	}
	if tmp, err := filepath.EvalSymlinks(os.TempDir()); err == nil {
		roots = append(roots, tmp)
	}
	// Check both the path as given and its target, so a symlink can't hide a hidden or ~/Library path
	// (for example ~/.zshrc pointing into a visible dotfiles repo).
	for _, p := range []string{filepath.Clean(mediaPath), resolved} {
		if err := checkSendableLocation(p, roots, home, mediaPath); err != nil {
			return "", err
		}
	}
	return resolved, nil
}

// checkSendableLocation errors unless path sits under one of roots, outside ~/Library, with no hidden component.
func checkSendableLocation(path string, roots []string, home, shown string) error {
	var rel string
	for _, root := range roots {
		if r, err := filepath.Rel(root, path); err == nil && r != "." && !strings.HasPrefix(r, "..") {
			rel = r
			if (r == "Library" || strings.HasPrefix(r, "Library/")) && home != "" && sameDir(root, home) {
				return fmt.Errorf("refusing to send files from ~/Library: %s", shown)
			}
			break
		}
	}
	if rel == "" {
		return fmt.Errorf("refusing to send files outside your home folder or temp dirs: %s", shown)
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if strings.HasPrefix(part, ".") {
			return fmt.Errorf("refusing to send files from hidden folders or hidden files: %s", shown)
		}
	}
	return nil
}

// sameDir reports whether a and b resolve to the same directory.
func sameDir(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// unsafeNameChars matches anything but letters, digits, space, dot, dash and underscore in a download filename.
var unsafeNameChars = regexp.MustCompile(`[^\p{L}\p{N} ._-]`)

// downloadFileName builds a flat `<name>_<id8><ext>` filename from a sender-chosen document title, stripping path
// separators, `..` and odd characters so the title can't steer the write outside the downloads dir.
func downloadFileName(filename, messageID, mediaType string) string {
	name := filepath.Base(strings.ReplaceAll(filename, "\\", "/"))
	name = unsafeNameChars.ReplaceAllString(name, "_")
	name = strings.TrimLeft(name, ". ")
	ext := filepath.Ext(name)
	if ext == "" {
		switch mediaType {
		case "image":
			ext = ".jpg"
		case "video":
			ext = ".mp4"
		case "audio":
			ext = ".ogg"
		case "document":
			ext = ".bin"
		case "sticker":
			ext = ".webp"
		}
	}
	base := strings.TrimSuffix(name, filepath.Ext(name))
	if r := []rune(base); len(r) > 100 {
		base = string(r[:100])
	}
	if base == "" {
		base = "media"
	}
	id := unsafeNameChars.ReplaceAllString(strings.ReplaceAll(messageID, ".", "_"), "_")
	if len(id) > 8 {
		id = id[:8]
	}
	return fmt.Sprintf("%s_%s%s", base, id, ext)
}

// extractDirectPathFromURL extracts the direct path component from a WhatsApp media URL.
func extractDirectPathFromURL(url string) string {
	parts := strings.SplitN(url, "/", 4)
	if len(parts) < 4 {
		return ""
	}
	return "/" + parts[3]
}

// analyzeOggOpus tries to extract duration and generate a simple waveform from an Ogg Opus file.
func analyzeOggOpus(data []byte) (duration uint32, waveform []byte, err error) {
	if len(data) < 4 || string(data[0:4]) != "OggS" {
		return 0, nil, fmt.Errorf("not a valid Ogg file (missing OggS signature)")
	}

	var lastGranule uint64
	var sampleRate uint32 = 48000
	var preSkip uint16
	var foundOpusHead bool

	for i := 0; i < len(data); {
		if i+27 >= len(data) {
			break
		}

		if string(data[i:i+4]) != "OggS" {
			i++
			continue
		}

		granulePos := binary.LittleEndian.Uint64(data[i+6 : i+14])
		pageSeqNum := binary.LittleEndian.Uint32(data[i+18 : i+22])
		numSegments := int(data[i+26])

		if i+27+numSegments >= len(data) {
			break
		}
		segmentTable := data[i+27 : i+27+numSegments]

		pageSize := 27 + numSegments
		for _, segLen := range segmentTable {
			pageSize += int(segLen)
		}

		if !foundOpusHead && pageSeqNum <= 1 {
			pageData := data[i : i+pageSize]
			headPos := bytes.Index(pageData, []byte("OpusHead"))
			if headPos >= 0 && headPos+12 < len(pageData) {
				headPos += 8
				if headPos+12 <= len(pageData) {
					preSkip = binary.LittleEndian.Uint16(pageData[headPos+10 : headPos+12])
					sampleRate = binary.LittleEndian.Uint32(pageData[headPos+12 : headPos+16])
					foundOpusHead = true
					fmt.Printf("Found OpusHead: sampleRate=%d, preSkip=%d\n", sampleRate, preSkip)
				}
			}
		}

		if granulePos != 0 {
			lastGranule = granulePos
		}

		i += pageSize
	}

	if !foundOpusHead {
		fmt.Println("Warning: OpusHead not found, using default values")
	}

	if lastGranule > 0 {
		durationSeconds := float64(lastGranule-uint64(preSkip)) / float64(sampleRate)
		duration = uint32(math.Ceil(durationSeconds))
		fmt.Printf("Calculated Opus duration from granule: %f seconds (lastGranule=%d)\n",
			durationSeconds, lastGranule)
	} else {
		fmt.Println("Warning: No valid granule position found, using estimation")
		durationEstimate := float64(len(data)) / 2000.0
		duration = uint32(durationEstimate)
	}

	if duration < 1 {
		duration = 1
	} else if duration > 300 {
		duration = 300
	}

	waveform = placeholderWaveform(duration)

	fmt.Printf("Ogg Opus analysis: size=%d bytes, calculated duration=%d sec, waveform=%d bytes\n",
		len(data), duration, len(waveform))

	return duration, waveform, nil
}

// placeholderWaveform generates a synthetic waveform for WhatsApp voice messages.
func placeholderWaveform(duration uint32) []byte {
	const waveformLength = 64
	waveform := make([]byte, waveformLength)

	baseAmplitude := 35.0
	frequencyFactor := float64(min(int(duration), 120)) / 30.0

	for i := range waveform {
		pos := float64(i) / float64(waveformLength)

		val := baseAmplitude * math.Sin(pos*math.Pi*frequencyFactor*8)
		val += (baseAmplitude / 2) * math.Sin(pos*math.Pi*frequencyFactor*16)

		val += (rand.Float64() - 0.5) * 15

		fadeInOut := math.Sin(pos * math.Pi)
		val = val * (0.7 + 0.3*fadeInOut)

		val = val + 50

		if val < 0 {
			val = 0
		} else if val > 100 {
			val = 100
		}

		waveform[i] = byte(val)
	}

	return waveform
}
