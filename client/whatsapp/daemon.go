package whatsapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"whatsapp-client/core"

	"github.com/mdp/qrterminal"
	"go.mau.fi/whatsmeow"
	//lint:ignore SA1019 whatsmeow/binary/proto is deprecated; keep aliases until migrated to waE2E
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// SocketFileName is the daemon's send/download API socket inside the data dir.
const SocketFileName = "daemon.sock"

type apiDownloadResponse struct {
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	Path      string `json:"path,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Filename  string `json:"filename,omitempty"`
}

// StartCore runs the WhatsApp client as a persistent daemon.
// It maintains a WebSocket connection to WhatsApp, syncs messages,
// and runs a REST API server.
func (w *Source) StartCore(ctx context.Context) error {
	logger := waLog.Stdout("Client", "INFO", true)
	logger.Infof("Starting WhatsApp client...")

	dbLog := waLog.Stdout("Database", "INFO", true)

	dir := w.dataDir
	if dir == "" {
		dir = core.DataDir()
	}
	if err := secureDataDir(dir); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	container, err := sqlstore.New(context.Background(), "sqlite", fmt.Sprintf("file:%s?_pragma=foreign_keys(on)&_pragma=busy_timeout(30000)", filepath.Join(dir, "whatsapp.db")), dbLog)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		if err == sql.ErrNoRows {
			deviceStore = container.NewDevice()
			logger.Infof("Created new device")
		} else {
			return fmt.Errorf("failed to get device: %w", err)
		}
	}

	client := whatsmeow.NewClient(deviceStore, logger)
	if client == nil {
		return fmt.Errorf("failed to create WhatsApp client")
	}

	messageStore, err := NewMessageStore(dir)
	if err != nil {
		return fmt.Errorf("failed to initialize message store: %w", err)
	}
	defer messageStore.Close()

	authReset := make(chan struct{}, 1)
	var authResetOnce sync.Once

	client.AddEventHandler(func(evt interface{}) {
		defer core.RecoverReport()
		reportSessionEvent(evt)
		switch v := evt.(type) {
		case *events.Message:
			handleMessage(client, messageStore, v, logger)
		case *events.HistorySync:
			handleHistorySync(client, messageStore, v, logger)
		case *events.Connected:
			logger.Infof("Connected to WhatsApp")
		case *events.LoggedOut:
			handleLoggedOut(logger, func() {
				authResetOnce.Do(func() {
					authReset <- struct{}{}
				})
			})
		}
	})

	connected := make(chan bool, 1)

	if client.Store.ID == nil {
		qrChan, _ := client.GetQRChannel(context.Background())
		err = client.Connect()
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}

		for evt := range qrChan {
			if evt.Event == "code" {
				fmt.Println("\nScan this QR code with your WhatsApp app:")
				qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
			} else if evt.Event == "success" {
				connected <- true
				break
			}
		}

		select {
		case <-connected:
			fmt.Println("\nSuccessfully connected and authenticated!")
		case <-time.After(3 * time.Minute):
			return fmt.Errorf("timeout waiting for QR code scan")
		}
	} else {
		err = client.Connect()
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		connected <- true
	}

	time.Sleep(2 * time.Second)

	if !client.IsConnected() {
		return fmt.Errorf("failed to establish stable connection")
	}

	fmt.Println("\n✓ Connected to WhatsApp!")

	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	go func() {
		defer core.RecoverReport()
		watchDisconnected(watchCtx, client.IsConnected, DisconnectReportAfter, time.Minute, reportLongDisconnect)
	}()

	restServer, err := startRESTServer(client, messageStore, dir)
	if err != nil {
		return err
	}
	defer restServer.Close()

	fmt.Println("REST server is running.")

	select {
	case <-ctx.Done():
		fmt.Println("Stopping WhatsApp client (context cancelled)...")
	case <-authReset:
		fmt.Println("Stopping WhatsApp client after session reset...")
	}
	client.Disconnect()
	return nil
}

// handleLoggedOut logs a hard auth reset and notifies any waiting shutdown path; the daemon idles until the next login.
func handleLoggedOut(logger waLog.Logger, notify func()) {
	logger.Warnf("Device session reset; run 'whatsapp-client login' to pair again")
	if notify != nil {
		notify()
	}
}

// handleMessage turns one live WhatsApp event into stored chat/message rows, plus group participant refreshes when relevant.
func handleMessage(client *whatsmeow.Client, messageStore *MessageStore, msg *events.Message, logger waLog.Logger) {
	if msg.Message == nil {
		return
	}

	chatJID := msg.Info.Chat.String()

	var sender string
	if msg.Info.IsFromMe {
		sender = client.Store.ID.User
	} else if msg.Info.Sender.User != "" {
		sender = msg.Info.Sender.User
	} else {
		sender = msg.Info.Chat.User
	}

	content := extractTextContent(msg.Message)
	mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength := extractMediaInfo(msg.Message)

	if content == "" && mediaType == "" {
		return
	}

	name := GetChatName(client, messageStore, msg.Info.Chat, chatJID, nil, msg.Info.PushName, logger)

	if msg.Info.IsGroup {
		go func() {
			defer core.RecoverReport()
			groupInfo, err := client.GetGroupInfo(context.Background(), msg.Info.Chat)
			if err == nil && len(groupInfo.Participants) > 0 {
				var jids []string
				for _, p := range groupInfo.Participants {
					jids = append(jids, p.JID.String())
				}
				messageStore.StoreGroupParticipants(chatJID, jids)
			}
		}()
	}

	messageStore.StoreChat(chatJID, name, msg.Info.Timestamp)

	err := messageStore.StoreMessage(
		msg.Info.ID,
		chatJID,
		sender,
		content,
		msg.Info.Timestamp,
		msg.Info.IsFromMe,
		mediaType,
		filename,
		url,
		mediaKey,
		fileSHA256,
		fileEncSHA256,
		fileLength,
	)

	if err != nil {
		logger.Warnf("Failed to store message: %v", err)
		core.ReportErr("store:message", err)
	}
}

// GetChatName resolves the best display name for a chat, upgrading placeholders from history/live metadata and mutating the chats table when improved.
func GetChatName(client *whatsmeow.Client, messageStore *MessageStore, jid types.JID, chatJID string, conversation interface{}, sender string, logger waLog.Logger) string {
	var existingName string
	err := messageStore.db.QueryRow("SELECT name FROM chats WHERE jid = ?", chatJID).Scan(&existingName)
	hasExisting := err == nil && existingName != ""

	if hasExisting && !looksLikePhoneNumber(existingName) && !looksLikeGroupPlaceholder(existingName) && !isSynthesizedName(existingName) {
		logger.Infof("Using existing chat name for %s: %s", chatJID, existingName)
		return existingName
	}

	var name string

	if jid.Server == "g.us" {
		logger.Infof("Getting name for group: %s", chatJID)

		if conversation != nil {
			var displayName, convName *string
			v := reflect.ValueOf(conversation)
			if v.Kind() == reflect.Ptr && !v.IsNil() {
				v = v.Elem()

				if displayNameField := v.FieldByName("DisplayName"); displayNameField.IsValid() && displayNameField.Kind() == reflect.Ptr && !displayNameField.IsNil() {
					dn := displayNameField.Elem().String()
					displayName = &dn
				}

				if nameField := v.FieldByName("Name"); nameField.IsValid() && nameField.Kind() == reflect.Ptr && !nameField.IsNil() {
					n := nameField.Elem().String()
					convName = &n
				}
			}

			if displayName != nil && *displayName != "" {
				name = *displayName
			} else if convName != nil && *convName != "" {
				name = *convName
			}
		}

		if name == "" {
			groupInfo, err := client.GetGroupInfo(context.Background(), jid)
			if err == nil && groupInfo.Name != "" {
				name = groupInfo.Name
			} else {
				name = fmt.Sprintf("Group %s", jid.User)
			}
		}

		logger.Infof("Using group name: %s", name)
	} else {
		logger.Infof("Getting name for contact: %s", chatJID)

		contact, err := client.Store.Contacts.GetContact(context.Background(), jid)
		if err == nil && contact.FullName != "" {
			name = contact.FullName
		} else if err == nil && contact.PushName != "" {
			name = contact.PushName
		} else if sender != "" {
			name = sender
		} else {
			name = jid.User
		}

		logger.Infof("Using contact name: %s", name)
	}

	isPlaceholder := looksLikePhoneNumber(existingName) || looksLikeGroupPlaceholder(existingName) || isSynthesizedName(existingName)
	if hasExisting && isPlaceholder && !looksLikePhoneNumber(name) && !looksLikeGroupPlaceholder(name) {
		logger.Infof("Upgrading chat name for %s: %s -> %s", chatJID, existingName, name)
		if _, err := messageStore.db.Exec("UPDATE chats SET name = ? WHERE jid = ?", name, chatJID); err != nil {
			core.ReportErr("store:chat_name", err)
		}
	}

	return name
}

// handleHistorySync stores backfilled chats, participants, and messages from history sync so search and MCP reads see older conversation data.
func handleHistorySync(client *whatsmeow.Client, messageStore *MessageStore, historySync *events.HistorySync, logger waLog.Logger) {
	fmt.Printf("Received history sync event with %d conversations\n", len(historySync.Data.Conversations))

	syncedCount := 0
	for _, conversation := range historySync.Data.Conversations {
		if conversation.ID == nil {
			continue
		}

		chatJID := *conversation.ID

		jid, err := types.ParseJID(chatJID)
		if err != nil {
			logger.Warnf("Failed to parse JID %s: %v", chatJID, err)
			continue
		}

		name := GetChatName(client, messageStore, jid, chatJID, conversation, "", logger)

		if jid.Server == "g.us" && len(conversation.GetParticipant()) > 0 {
			var pJIDs []string
			for _, gp := range conversation.GetParticipant() {
				if gp.GetUserJID() != "" {
					pJIDs = append(pJIDs, gp.GetUserJID())
				}
			}
			if len(pJIDs) > 0 {
				messageStore.StoreGroupParticipants(chatJID, pJIDs)
			}
		}

		messages := conversation.Messages
		if len(messages) > 0 {
			latestMsg := messages[0]
			if latestMsg == nil || latestMsg.Message == nil {
				continue
			}

			ts := latestMsg.Message.GetMessageTimestamp()
			if ts == 0 {
				continue
			}
			chatTimestamp := time.Unix(int64(ts), 0)

			messageStore.StoreChat(chatJID, name, chatTimestamp)

			for _, msg := range messages {
				if msg == nil || msg.Message == nil {
					continue
				}

				var content string
				if msg.Message.Message != nil {
					content = extractTextContent(msg.Message.Message)
				}

				var mediaType, filename, url string
				var mediaKey, fileSHA256, fileEncSHA256 []byte
				var fileLength uint64

				if msg.Message.Message != nil {
					mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength = extractMediaInfo(msg.Message.Message)
				}

				if content == "" && mediaType == "" {
					continue
				}

				var sender string
				isFromMe := false
				if msg.Message.Key != nil {
					if msg.Message.Key.FromMe != nil {
						isFromMe = *msg.Message.Key.FromMe
					}
				}
				if isFromMe {
					sender = client.Store.ID.User
				} else {
					if msg.Message.Key != nil && msg.Message.Key.Participant != nil && *msg.Message.Key.Participant != "" {
						sender = *msg.Message.Key.Participant
					}
					if sender == "" && msg.Message.Participant != nil && *msg.Message.Participant != "" {
						sender = *msg.Message.Participant
					}
					if sender == "" && msg.Message.PushName != nil && *msg.Message.PushName != "" {
						sender = *msg.Message.PushName
					}
					if sender == "" {
						sender = jid.User
					}
				}

				msgID := ""
				if msg.Message.Key != nil && msg.Message.Key.ID != nil {
					msgID = *msg.Message.Key.ID
				}

				msgTs := msg.Message.GetMessageTimestamp()
				if msgTs == 0 {
					continue
				}
				msgTime := time.Unix(int64(msgTs), 0)

				err = messageStore.StoreMessage(
					msgID,
					chatJID,
					sender,
					content,
					msgTime,
					isFromMe,
					mediaType,
					filename,
					url,
					mediaKey,
					fileSHA256,
					fileEncSHA256,
					fileLength,
				)
				if err != nil {
					logger.Warnf("Failed to store history message: %v", err)
					core.ReportErr("store:history_message", err)
				} else {
					syncedCount++
				}
			}
		}
	}

	fmt.Printf("History sync complete. Stored %d messages.\n", syncedCount)
}

// startRESTServer launches the daemon-side write API on the 0600 Unix socket in dataDir, exposing send/download endpoints that
// proxy to the live WhatsApp client. The caller closes the returned server when the client stops so a reconnect can listen again.
func startRESTServer(client *whatsmeow.Client, messageStore *MessageStore, dataDir string) (*http.Server, error) {
	startTime := time.Now()
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]interface{}{
			"status":    "ok",
			"connected": client.IsConnected(),
			"uptime_s":  int(time.Since(startTime).Seconds()),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/api/send", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req SendMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		success, message := sendWhatsAppMessage(client, req.Recipient, req.Message, req.MediaPath)

		resp := SendMessageResponse{
			Success: success,
			Message: message,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/api/download", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			MessageID string `json:"message_id"`
			ChatJID   string `json:"chat_jid"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if req.MessageID == "" || req.ChatJID == "" {
			http.Error(w, "Missing message_id or chat_jid parameter", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		success, filePath, mediaType, filename, err := downloadMedia(client, messageStore, req.MessageID, req.ChatJID, dataDir)
		if !success {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(apiDownloadResponse{Success: false, Message: fmt.Sprintf("failed to download: %v", err)})
			return
		}
		json.NewEncoder(w).Encode(apiDownloadResponse{
			Success:   true,
			Message:   "downloaded",
			Path:      filePath,
			MediaType: mediaType,
			Filename:  filename,
		})
	})

	ln, err := listenSocket(filepath.Join(dataDir, SocketFileName))
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Handler: recoverHandler(mux)}
	go func() {
		slog.Info("starting REST API server", "socket", ln.Addr().String())
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("REST server error", "err", err)
			core.ReportErr("daemon:rest", err)
		}
	}()
	return srv, nil
}

// listenSocket listens on a Unix socket at path readable and writable by the owner only, replacing a stale socket file.
func listenSocket(path string) (net.Listener, error) {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s exists and is not a socket", path)
		}
		os.Remove(path)
	}
	// The data dir is 0700 (secureDataDir), so the socket is private even before the chmod below.
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil { // nocov
		ln.Close()
		return nil, err
	}
	return ln, nil
}

// extractTextContent normalizes many WhatsApp protobuf message shapes into the text that live/history ingest stores and indexes.
//
//lint:ignore SA1019 waProto.Message is a deprecated alias to waE2E.Message
func extractTextContent(msg *waProto.Message) string {
	if msg == nil {
		return ""
	}

	if text := msg.GetConversation(); text != "" {
		return text
	}
	if ext := msg.GetExtendedTextMessage(); ext != nil {
		return ext.GetText()
	}
	if msg.GetStickerMessage() != nil {
		return "sticker"
	}
	if c := msg.GetContactMessage(); c != nil {
		if dn := c.GetDisplayName(); dn != "" {
			return "contact: " + dn
		}
		return "contact"
	}
	if msg.GetContactsArrayMessage() != nil {
		return "contacts"
	}
	if loc := msg.GetLocationMessage(); loc != nil {
		return fmt.Sprintf("location: %.4f, %.4f", loc.GetDegreesLatitude(), loc.GetDegreesLongitude())
	}
	if msg.GetLiveLocationMessage() != nil {
		return "live location"
	}
	if inv := msg.GetGroupInviteMessage(); inv != nil {
		if gn := inv.GetGroupName(); gn != "" {
			return "group invite: " + gn
		}
		return "group invite"
	}
	if vo := msg.GetViewOnceMessage(); vo != nil && vo.GetMessage() != nil {
		inner := extractTextContent(vo.GetMessage())
		if inner != "" {
			return inner
		}
		return "view-once message"
	}
	if eph := msg.GetEphemeralMessage(); eph != nil && eph.GetMessage() != nil {
		inner := extractTextContent(eph.GetMessage())
		if inner != "" {
			return inner
		}
	}
	if msg.GetListMessage() != nil {
		return "list"
	}
	if msg.GetListResponseMessage() != nil {
		return "list response"
	}
	if r := msg.GetReactionMessage(); r != nil {
		return "reaction: " + r.GetText()
	}
	if msg.GetButtonsMessage() != nil {
		return "buttons"
	}
	if msg.GetButtonsResponseMessage() != nil {
		return "button response"
	}
	if msg.GetPollCreationMessage() != nil || msg.GetPollCreationMessageV2() != nil || msg.GetPollCreationMessageV3() != nil {
		return "poll"
	}
	if msg.GetPollUpdateMessage() != nil {
		return "poll update"
	}

	return ""
}

// extractMediaInfo pulls download metadata from supported media message types so later REST-triggered downloads can be reconstructed.
//
//lint:ignore SA1019 waProto.Message is a deprecated alias to waE2E.Message
func extractMediaInfo(msg *waProto.Message) (mediaType string, filename string, url string, mediaKey []byte, fileSHA256 []byte, fileEncSHA256 []byte, fileLength uint64) {
	if msg == nil {
		return "", "", "", nil, nil, nil, 0
	}

	if img := msg.GetImageMessage(); img != nil {
		return "image", "image.jpg", img.GetURL(), img.MediaKey, img.FileSHA256, img.FileEncSHA256, img.GetFileLength()
	}
	if audio := msg.GetAudioMessage(); audio != nil {
		return "audio", "audio.ogg", audio.GetURL(), audio.MediaKey, audio.FileSHA256, audio.FileEncSHA256, audio.GetFileLength()
	}
	if vid := msg.GetVideoMessage(); vid != nil {
		return "video", "video.mp4", vid.GetURL(), vid.MediaKey, vid.FileSHA256, vid.FileEncSHA256, vid.GetFileLength()
	}
	if doc := msg.GetDocumentMessage(); doc != nil {
		fn := doc.GetTitle()
		if fn == "" {
			fn = "document"
		}
		return "document", fn, doc.GetURL(), doc.MediaKey, doc.FileSHA256, doc.FileEncSHA256, doc.GetFileLength()
	}
	if sticker := msg.GetStickerMessage(); sticker != nil {
		return "sticker", "sticker.webp", sticker.GetURL(), sticker.MediaKey, sticker.FileSHA256, sticker.FileEncSHA256, sticker.GetFileLength()
	}

	return "", "", "", nil, nil, nil, 0
}

// InfoLines returns indented status lines for the WhatsApp session and message databases.
func InfoLines(dDir string) []string {
	var lines []string
	if IsLoggedIn(dDir) {
		lines = append(lines, "   Session:    "+SessionAuthDisplay(dDir))
	} else {
		lines = append(lines, "   Session:    not paired (run 'whatsapp-client login')")
	}

	var dbSize int64
	waDB := filepath.Join(dDir, "whatsapp.db")
	if _, err := os.Stat(waDB); err == nil {
		dbSize += core.FileGroupSizeBytes(waDB)
	}

	msgDB := filepath.Join(dDir, "messages.db")
	hasMsgDB := false
	if _, err := os.Stat(msgDB); err == nil {
		hasMsgDB = true
		dbSize += core.FileGroupSizeBytes(msgDB)
	}

	if dbSize > 0 {
		lines = append(lines, fmt.Sprintf("   DB:         %s", core.FormatSizeMB(dbSize)))
	}

	if !hasMsgDB {
		lines = append(lines, "   Messages:   no database yet")
		return lines
	}
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(30000)", msgDB))
	if err != nil { // nocov
		return append(lines, "   Messages:   unable to read database")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	var chatCount, msgCount int
	errChats := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM chats").Scan(&chatCount)
	errMsgs := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM messages").Scan(&msgCount)
	if errChats != nil || errMsgs != nil {
		return append(lines, "   Messages:   unable to read database")
	}
	return append(lines, fmt.Sprintf("   Messages:   %d across %d chats", msgCount, chatCount))
}

// recoverHandler reports a handler panic, then re-panics so net/http logs and drops the connection as before.
func recoverHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer core.RecoverReport()
		next.ServeHTTP(w, r)
	})
}
