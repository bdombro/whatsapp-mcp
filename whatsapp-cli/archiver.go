package main

import (
	"bufio"
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"strings"
	"time"
)

const ownerName = "Brian Dombrowski"

type archivedChat struct {
	jid      string
	name     string
	msgCount int
}

// messageLine is a formatted line ready to be written, with a parsed timestamp for sorting.
type messageLine struct {
	ts   time.Time
	line string
}

// parseArchiveDate parses YYYY.MM.DD or YYYY.MM.DD:HH.MM format.
func parseArchiveDate(s string) (time.Time, error) {
	datePart := s
	timePart := ""
	if idx := strings.Index(s, ":"); idx != -1 {
		datePart = s[:idx]
		timePart = s[idx+1:]
	}

	dParts := strings.Split(datePart, ".")
	if len(dParts) != 3 {
		return time.Time{}, fmt.Errorf("expected YYYY.MM.DD or YYYY.MM.DD:HH.MM, got %q", s)
	}
	year, err1 := strconv.Atoi(dParts[0])
	month, err2 := strconv.Atoi(dParts[1])
	day, err3 := strconv.Atoi(dParts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return time.Time{}, fmt.Errorf("invalid date %q: non-numeric components", s)
	}

	hour, min := 0, 0
	if timePart != "" {
		tParts := strings.Split(timePart, ".")
		if len(tParts) != 2 {
			return time.Time{}, fmt.Errorf("expected HH.MM after colon, got %q", timePart)
		}
		var err4, err5 error
		hour, err4 = strconv.Atoi(tParts[0])
		min, err5 = strconv.Atoi(tParts[1])
		if err4 != nil || err5 != nil {
			return time.Time{}, fmt.Errorf("invalid time %q: non-numeric components", timePart)
		}
	}
	return time.Date(year, time.Month(month), day, hour, min, 0, 0, time.Local), nil
}

func hasTimePart(s string) bool {
	return strings.Contains(s, ":")
}

func jidToFilename(jid string) string {
	if jid == "" {
		return "unknown_chat.txt"
	}
	// Replace @ and . to produce a clean flat filename
	name := strings.ReplaceAll(jid, "@", "_")
	name = strings.ReplaceAll(name, ".", "_")
	return name + ".txt"
}

// parseMessageTimestamp extracts a time from a line like "[2026-03-28 10:40:22] ...".
// Returns zero time and false if the line isn't a message.
func parseMessageTimestamp(line string) (time.Time, bool) {
	if len(line) < 21 || line[0] != '[' {
		return time.Time{}, false
	}
	closeIdx := strings.Index(line, "]")
	if closeIdx < 2 {
		return time.Time{}, false
	}
	tsStr := line[1:closeIdx]
	t, err := time.Parse("2006-01-02 15:04:05", tsStr)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// readRetainedMessages reads an existing archive .txt file and returns message
// lines whose timestamps fall OUTSIDE the [from, to] window.
func readRetainedMessages(path string, from, to time.Time) []messageLine {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var retained []messageLine
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		ts, ok := parseMessageTimestamp(line)
		if !ok {
			continue
		}
		if ts.Before(from) || ts.After(to) {
			retained = append(retained, messageLine{ts: ts, line: line})
		}
	}
	return retained
}

// renameStaleFile scans outputDir for an existing .txt file that belongs to
// the same chat (identified by its JID in the file header) but has an outdated
// filename. If found and different from newFilename, it is renamed.
func renameStaleFile(outputDir, chatJID, newFilename string) {
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return
	}

	phonePart := chatJID
	if idx := strings.Index(chatJID, "@"); idx != -1 {
		phonePart = chatJID[:idx]
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") || entry.Name() == newFilename {
			continue
		}

		nameWithoutExt := strings.TrimSuffix(entry.Name(), ".txt")
		if nameWithoutExt == phonePart || nameWithoutExt == chatJID {
			oldPath := filepath.Join(outputDir, entry.Name())
			newPath := filepath.Join(outputDir, newFilename)
			fmt.Printf("  Renaming %s -> %s\n", entry.Name(), newFilename)
			os.Rename(oldPath, newPath)
			return
		}

		oldPath := filepath.Join(outputDir, entry.Name())
		if fileContainsChatJID(oldPath, chatJID) {
			newPath := filepath.Join(outputDir, newFilename)
			fmt.Printf("  Renaming %s -> %s\n", entry.Name(), newFilename)
			os.Rename(oldPath, newPath)
			return
		}
	}
}

func fileContainsChatJID(path, chatJID string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	return n > 0 && strings.Contains(string(buf[:n]), "CHAT JID: "+chatJID)
}

// enhanceAllFiles scans every .txt archive file and replaces phone-number
// names with resolved contact names in the chat title, participant list,
// and message sender fields. Renames files whose chat name improves.
func enhanceAllFiles(store *MessageStore, outputDir string) {
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}
		fp := filepath.Join(outputDir, entry.Name())
		enhanceFile(store, fp)
	}
}

func enhanceFile(store *MessageStore, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(string(data), "\n")
	changed := false
	var chatJID string

	// First pass: resolve phone-number names and expand group participants.
	// Because expanding a group participant produces multiple lines, we rebuild
	// the slice in a separate output buffer.
	var result []string
	for i, line := range lines {
		// CHAT: <name>
		if strings.HasPrefix(line, "CHAT: ") {
			name := strings.TrimSpace(strings.TrimPrefix(line, "CHAT: "))
			if looksLikePhoneNumber(name) || looksLikeGroupPlaceholder(name) || isSynthesizedName(name) {
				if i+1 < len(lines) && strings.HasPrefix(lines[i+1], "CHAT JID: ") {
					chatJID = strings.TrimSpace(strings.TrimPrefix(lines[i+1], "CHAT JID: "))
					var dbName sql.NullString
					store.db.QueryRow("SELECT name FROM chats WHERE jid = ?", chatJID).Scan(&dbName)
					resolved := ""
					if dbName.Valid && dbName.String != "" && !looksLikePhoneNumber(dbName.String) && !looksLikeGroupPlaceholder(dbName.String) {
						resolved = dbName.String
					} else {
						resolved = store.GetSenderName(chatJID)
					}
					if resolved != name && !looksLikePhoneNumber(resolved) && !looksLikeGroupPlaceholder(resolved) && resolved != chatJID {
						result = append(result, "CHAT: "+resolved)
						changed = true
						continue
					}
				}
			}
			result = append(result, line)
			continue
		}

		// CHAT JID: <jid> — capture for later use
		if strings.HasPrefix(line, "CHAT JID: ") {
			chatJID = strings.TrimSpace(strings.TrimPrefix(line, "CHAT JID: "))
			result = append(result, line)
			continue
		}

		// Strip deprecated SYNC RANGE lines from older archives
		if strings.HasPrefix(line, "SYNC RANGE: ") {
			changed = true
			continue
		}

		// Participant line: "  - SomeName (phone)" or "  - phone (phone)"
		if strings.HasPrefix(line, "  - ") && !strings.HasPrefix(line, "  - "+ownerName) {
			expanded := expandGroupParticipantLine(store, line)
			if expanded != nil {
				result = append(result, expanded...)
				changed = true
				continue
			}
			enhanced := enhanceParticipantLine(store, line)
			if enhanced != line {
				result = append(result, enhanced)
				changed = true
				continue
			}
			result = append(result, line)
			continue
		}

		// Message line: "[timestamp] SenderName: content"
		if ts, ok := parseMessageTimestamp(line); ok && ts != (time.Time{}) {
			enhanced := enhanceMessageLine(store, line, chatJID)
			if enhanced != line {
				result = append(result, enhanced)
				changed = true
				continue
			}
		}

		result = append(result, line)
	}

	if !changed {
		return
	}

	if err := os.WriteFile(path, []byte(strings.Join(result, "\n")), 0644); err != nil {
		return
	}

	fmt.Printf("  Enhanced names in: %s\n", filepath.Base(path))
}

// expandGroupParticipantLine checks if a participant line refers to a group
// rather than an individual. If so, it returns replacement lines listing the
// group's actual members. Returns nil if the participant is not a group.
func expandGroupParticipantLine(store *MessageStore, line string) []string {
	content := strings.TrimPrefix(line, "  - ")

	// Extract a candidate ID to check against group JIDs.
	// Handles both old format "Name (phone)" and new format "Name; phone; lid".
	var phone string
	if strings.Contains(content, "; ") {
		for _, part := range strings.Split(content, "; ") {
			part = strings.TrimSpace(part)
			if looksLikePhoneNumber(part) {
				phone = part
				break
			}
		}
	} else {
		parenIdx := strings.LastIndex(content, " (")
		if parenIdx >= 0 {
			phone = strings.TrimSuffix(content[parenIdx+2:], ")")
		}
	}
	if phone == "" {
		return nil
	}

	groupJID := phone + "@g.us"
	if !store.isGroupSender(phone) {
		return nil
	}

	members := store.GetGroupParticipants(groupJID)
	if len(members) == 0 {
		return nil
	}

	var expanded []string
	seen := make(map[string]struct{})
	for _, m := range members {
		info := store.ResolveParticipant(m)
		if info.Name == ownerName {
			continue
		}
		key := m
		if info.Phone != "" {
			key = info.Phone
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		expanded = append(expanded, FormatParticipantLine(info))
	}
	sort.Strings(expanded)
	return expanded
}

// enhanceParticipantLine re-resolves a participant line to the new
// "Name; phone; lid" format. Handles both old-style "  - Name (phone)" lines
// and new-style "  - Name; phone; lid" lines.
func enhanceParticipantLine(store *MessageStore, line string) string {
	content := strings.TrimPrefix(line, "  - ")

	// Detect format: new-style uses semicolons, old-style uses parenthesised phone.
	if strings.Contains(content, "; ") {
		return enhanceNewFormatLine(store, content, line)
	}
	return enhanceOldFormatLine(store, content, line)
}

func enhanceNewFormatLine(store *MessageStore, content, original string) string {
	parts := strings.Split(content, "; ")

	var existingName, existingPhone, existingLID string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, "lid:") {
			existingLID = strings.TrimPrefix(p, "lid:")
		} else if strings.HasSuffix(p, "@lid") {
			existingLID = strings.TrimSuffix(p, "@lid")
		} else if looksLikePhoneNumber(p) {
			existingPhone = p
		} else if !looksLikeUnresolved(p) {
			existingName = p
		}
	}

	var jid string
	if existingLID != "" {
		jid = existingLID + "@lid"
	} else if existingPhone != "" {
		jid = existingPhone + "@s.whatsapp.net"
	} else {
		return original
	}

	info := store.ResolveParticipant(jid)
	if info.Name == "" && existingName != "" {
		info.Name = existingName
	}
	newLine := FormatParticipantLine(info)
	if newLine == original {
		return original
	}
	return newLine
}

func enhanceOldFormatLine(store *MessageStore, content, original string) string {
	parenIdx := strings.LastIndex(content, " (")
	if parenIdx < 0 {
		return original
	}
	name := content[:parenIdx]
	phone := strings.TrimSuffix(content[parenIdx+2:], ")")

	// Build a JID to resolve fully
	var jid string
	if strings.HasSuffix(name, "@lid") || strings.HasSuffix(phone, "@lid") {
		lid := phone
		if strings.HasSuffix(name, "@lid") {
			lid = strings.TrimSuffix(name, "@lid")
		}
		jid = lid + "@lid"
	} else {
		jid = phone + "@s.whatsapp.net"
	}

	info := store.ResolveParticipant(jid)
	if info.Name == "" && !looksLikeUnresolved(name) {
		info.Name = name
	}
	newLine := FormatParticipantLine(info)
	if newLine == original {
		return original
	}
	return newLine
}

// enhanceMessageLine resolves phone-number sender names and replaces
// group-name senders with "(group member)" in a message line.
// Input format: "[TIMESTAMP] SENDER: CONTENT"
func enhanceMessageLine(store *MessageStore, line string, chatJID string) string {
	closeIdx := strings.Index(line, "] ")
	if closeIdx < 0 {
		return line
	}
	rest := line[closeIdx+2:]
	colonIdx := strings.Index(rest, ": ")
	if colonIdx < 0 {
		return line
	}
	sender := rest[:colonIdx]

	// Detect group-name-as-sender: the sender matches the chat name or is a
	// group placeholder like "Group 120363...".
	if chatJID != "" && isGroupJID(chatJID) {
		chatNumeric := strings.TrimSuffix(chatJID, "@g.us")
		var chatName sql.NullString
		store.db.QueryRow("SELECT name FROM chats WHERE jid = ?", chatJID).Scan(&chatName)
		if sender == chatNumeric ||
			looksLikeGroupPlaceholder(sender) ||
			(chatName.Valid && sender == chatName.String) {
			return line[:closeIdx+2] + "(group member): " + rest[colonIdx+2:]
		}
	}

	if !looksLikePhoneNumber(sender) {
		return line
	}

	// Check if this phone number is actually a group JID masquerading as sender
	if store.isGroupSender(sender) {
		return line[:closeIdx+2] + "(group member): " + rest[colonIdx+2:]
	}

	resolved := store.GetSenderName(sender)
	if looksLikePhoneNumber(resolved) || resolved == sender {
		resolved = store.GetSenderName(sender + "@s.whatsapp.net")
	}
	if looksLikePhoneNumber(resolved) || resolved == sender || resolved == sender+"@s.whatsapp.net" {
		return line
	}
	return line[:closeIdx+2] + resolved + ": " + rest[colonIdx+2:]
}

// cleanseAllFiles removes messages within [from, to] from every .txt in
// outputDir. Files left with zero messages are deleted.
func cleanseAllFiles(outputDir string, from, to time.Time) {
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}

		fp := filepath.Join(outputDir, entry.Name())
		retained := readRetainedMessages(fp, from, to)

		if len(retained) == 0 {
			// Read the header to check if there were any messages at all
			// (don't delete a file that only has a header and messages all within the window)
			os.Remove(fp)
			continue
		}

		// Rewrite the file keeping only retained messages, preserving header
		rewriteFileMessages(fp, retained)
	}
}

// rewriteFileMessages rewrites a .txt archive file, keeping the original header
// but replacing the message body with the given lines.
func rewriteFileMessages(path string, msgs []messageLine) {
	f, err := os.Open(path)
	if err != nil {
		return
	}

	var headerLines []string
	scanner := bufio.NewScanner(f)
	headerDone := false
	eqCount := 0
	for scanner.Scan() {
		line := scanner.Text()
		headerLines = append(headerLines, line)
		if strings.HasPrefix(line, strings.Repeat("=", 20)) {
			eqCount++
			if eqCount >= 2 {
				headerDone = true
				break
			}
		}
	}
	f.Close()

	if !headerDone {
		return
	}

	out, err := os.Create(path)
	if err != nil {
		return
	}
	defer out.Close()

	for _, h := range headerLines {
		fmt.Fprintln(out, h)
	}
	fmt.Fprintln(out)

	sort.Slice(msgs, func(i, j int) bool { return msgs[i].ts.Before(msgs[j].ts) })
	for _, m := range msgs {
		fmt.Fprintln(out, m.line)
	}
}

// populateFromWhatsApp connects to WhatsApp, triggers a history sync,
// waits for messages to arrive, and stores them in messages.db so that
// sync can work even if the core daemon has never run.
func populateFromWhatsApp(messageStore *MessageStore, w io.Writer) error {
	dir := dataDir()

	logger := waLog.Stdout("Sync", "WARN", true)
	dbLog := waLog.Stdout("Database", "WARN", true)

	container, err := sqlstore.New(context.Background(), "sqlite3",
		fmt.Sprintf("file:%s?_foreign_keys=on", filepath.Join(dir, "whatsapp.db")), dbLog)
	if err != nil {
		return fmt.Errorf("failed to open whatsapp database: %v", err)
	}

	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("not logged in – run 'whatsapp-cli login' first")
		}
		return fmt.Errorf("failed to get device: %v", err)
	}

	client := whatsmeow.NewClient(deviceStore, logger)
	if client == nil {
		return fmt.Errorf("failed to create WhatsApp client")
	}
	if client.Store.ID == nil {
		return fmt.Errorf("not logged in – run 'whatsapp-cli login' first")
	}

	// Track history sync completion
	var historySyncCount int
	var mu sync.Mutex

	// Timer that fires when no new history sync events arrive for a while
	idleTimer := time.NewTimer(30 * time.Second)
	idleTimer.Stop()

	client.AddEventHandler(func(evt interface{}) {
		switch v := evt.(type) {
		case *events.HistorySync:
			handleHistorySync(client, messageStore, v, logger)
			mu.Lock()
			historySyncCount++
			mu.Unlock()
			// Reset idle timer – more data may follow
			idleTimer.Reset(15 * time.Second)
		case *events.Message:
			// Also capture live messages that arrive during syncing
			handleMessage(client, messageStore, v, logger)
			mu.Lock()
			historySyncCount++
			mu.Unlock()
			idleTimer.Reset(15 * time.Second)
		case *events.Connected:
			fmt.Fprintf(w, "Connected to WhatsApp, waiting for history sync...\n")
			// Start idle timer once connected
			idleTimer.Reset(45 * time.Second)
		}
	})

	if err := client.Connect(); err != nil {
		return fmt.Errorf("failed to connect to WhatsApp: %v", err)
	}
	defer client.Disconnect()

	// Wait for either the idle timer (no more sync events) or a hard deadline
	hardDeadline := time.NewTimer(2 * time.Minute)
	defer hardDeadline.Stop()

	select {
	case <-idleTimer.C:
		// No new history sync events for a while – we're done
	case <-hardDeadline.C:
		fmt.Fprintf(w, "Reached time limit waiting for history sync.\n")
	}

	mu.Lock()
	count := historySyncCount
	mu.Unlock()

	if count == 0 {
		fmt.Fprintf(w, "No history sync data received from the server.\n")
		fmt.Fprintf(w, "WhatsApp only sends history during initial pairing.\n")
		fmt.Fprintf(w, "To populate the database, run:\n")
		fmt.Fprintf(w, "  whatsapp-cli login --relogin\n")
		fmt.Fprintf(w, "Then re-run your sync command.\n")
	} else {
		fmt.Fprintf(w, "Received %d history sync event(s).\n", count)
	}

	fetchGroupParticipants(client, messageStore, w)

	return nil
}

// refreshGroupData connects to WhatsApp to fetch/update group member lists
// and resolve placeholder names for any group chats that need it.
func refreshGroupData(messageStore *MessageStore, w io.Writer) {
	var missingParticipants int
	messageStore.db.QueryRow(`
		SELECT COUNT(*) FROM chats c
		WHERE c.jid LIKE '%@g.us'
		AND NOT EXISTS (SELECT 1 FROM group_participants gp WHERE gp.group_jid = c.jid)
	`).Scan(&missingParticipants)

	var placeholderNames int
	messageStore.db.QueryRow(`
		SELECT COUNT(*) FROM chats
		WHERE jid LIKE '%@g.us' AND name LIKE 'Group %'
	`).Scan(&placeholderNames)

	if missingParticipants == 0 && placeholderNames == 0 {
		return
	}

	fmt.Fprintf(w, "Groups needing refresh: %d missing participants, %d placeholder names. Connecting to WhatsApp...\n", missingParticipants, placeholderNames)

	dir := dataDir()
	logger := waLog.Stdout("GroupSync", "WARN", true)
	dbLog := waLog.Stdout("Database", "WARN", true)

	container, err := sqlstore.New(context.Background(), "sqlite3",
		fmt.Sprintf("file:%s?_foreign_keys=on", filepath.Join(dir, "whatsapp.db")), dbLog)
	if err != nil {
		fmt.Fprintf(w, "Could not open WhatsApp DB for group sync: %v\n", err)
		return
	}

	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		return
	}

	client := whatsmeow.NewClient(deviceStore, logger)
	if client == nil || client.Store.ID == nil {
		return
	}

	if err := client.Connect(); err != nil {
		return
	}
	defer client.Disconnect()

	time.Sleep(2 * time.Second)

	fetchGroupParticipants(client, messageStore, w)
}

// fetchGroupParticipants queries WhatsApp for the member list of every known
// group chat and stores the results in the group_participants table.
// It first uses GetJoinedGroups (a single bulk API call) to get complete info
// for all currently joined groups, then falls back to individual GetGroupInfo
// calls for any remaining groups (e.g. ones the user has left).
func fetchGroupParticipants(client *whatsmeow.Client, store *MessageStore, w io.Writer) {
	rows, err := store.db.Query("SELECT jid FROM chats WHERE jid LIKE '%@g.us'")
	if err != nil {
		return
	}
	defer rows.Close()

	groupJIDSet := make(map[string]bool)
	for rows.Next() {
		var jid string
		if rows.Scan(&jid) == nil {
			groupJIDSet[jid] = true
		}
	}

	if len(groupJIDSet) == 0 {
		return
	}

	fmt.Fprintf(w, "Fetching info for %d group(s)...\n", len(groupJIDSet))
	fetched := 0
	namesFixed := 0

	// Phase 1: Bulk fetch all joined groups in a single API call.
	// This returns complete data (name + participants) and is far more
	// reliable than individual GetGroupInfo calls.
	joinedGroups, err := client.GetJoinedGroups(context.Background())
	if err != nil {
		fmt.Fprintf(w, "  GetJoinedGroups failed: %v — falling back to individual queries\n", err)
	} else {
		namedCount := 0
		for _, info := range joinedGroups {
			gjid := info.JID.String()
			if !groupJIDSet[gjid] {
				continue
			}
			delete(groupJIDSet, gjid)

			var pJIDs []string
			for _, p := range info.Participants {
				pJIDs = append(pJIDs, p.JID.String())
			}
			if len(pJIDs) > 0 {
				store.StoreGroupParticipants(gjid, pJIDs)
				fetched++
			}
			if info.Name != "" {
				namedCount++
				if store.upgradeGroupName(gjid, info.Name) {
					namesFixed++
				}
			}
		}
		fmt.Fprintf(w, "  Bulk: resolved %d joined group(s), %d had names, %d placeholder(s) upgraded.\n", fetched, namedCount, namesFixed)
	}

	// Phase 2: Individual queries for groups not in the joined list
	// (left groups, or groups GetJoinedGroups didn't cover).
	if len(groupJIDSet) > 0 {
		phase2Fetched := 0
		phase2Names := 0
		apiErrors := 0
		for gjid := range groupJIDSet {
			parsed, parseErr := types.ParseJID(gjid)
			if parseErr != nil {
				continue
			}
			info, infoErr := client.GetGroupInfo(context.Background(), parsed)
			if infoErr != nil {
				apiErrors++
				continue
			}
			var pJIDs []string
			for _, p := range info.Participants {
				pJIDs = append(pJIDs, p.JID.String())
			}
			if len(pJIDs) > 0 {
				store.StoreGroupParticipants(gjid, pJIDs)
				phase2Fetched++
			}
			if info.Name != "" {
				if store.upgradeGroupName(gjid, info.Name) {
					phase2Names++
				}
			}
		}
		fetched += phase2Fetched
		namesFixed += phase2Names
		fmt.Fprintf(w, "  Individual: resolved %d remaining group(s), %d name(s) upgraded, %d errors.\n", phase2Fetched, phase2Names, apiErrors)
	}

	fmt.Fprintf(w, "Stored participants for %d group(s), resolved %d name(s) total.\n", fetched, namesFixed)

	// Phase 3: For any groups still stuck with placeholder names, synthesize
	// a descriptive name from the resolved participant list.
	synthRows, err := store.db.Query(`
		SELECT jid FROM chats
		WHERE jid LIKE '%@g.us' AND name LIKE 'Group %'
	`)
	if err != nil {
		return
	}
	defer synthRows.Close()

	synthFixed := 0
	for synthRows.Next() {
		var gjid string
		if synthRows.Scan(&gjid) != nil {
			continue
		}
		synthName := store.synthesizeGroupName(gjid)
		if synthName != "" && store.upgradeGroupName(gjid, synthName) {
			synthFixed++
		}
	}
	if synthFixed > 0 {
		fmt.Fprintf(w, "  Synthesized %d name(s) from participant lists.\n", synthFixed)
	}
}

func runSync(args []string) {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	catchup := fs.Bool("catchup", false, "Catch up from the last archived message to now")
	deleteRange := fs.Bool("delete", false, "Delete archived messages between --from and --to (requires --from)")
	fromStr := fs.String("from", "", "Start date/time: YYYY.MM.DD or YYYY.MM.DD:HH.MM (required unless --catchup)")
	toStr := fs.String("to", "", "End date/time: YYYY.MM.DD or YYYY.MM.DD:HH.MM (default: today)")
	outputDir := fs.String("output", filepath.Join(dataDir(), "chats"), "Output directory for archived chat files")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: whatsapp-cli sync [flags]\n\nFlags:\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  whatsapp-cli sync --catchup\n")
		fmt.Fprintf(os.Stderr, "  whatsapp-cli sync --from=2026.03.01\n")
		fmt.Fprintf(os.Stderr, "  whatsapp-cli sync --from=2026.03.01 --to=2026.03.28\n")
		fmt.Fprintf(os.Stderr, "  whatsapp-cli sync --from=2026.03.01:09.00 --to=2026.03.01:17.00\n")
		fmt.Fprintf(os.Stderr, "  whatsapp-cli sync --delete --from=2026.03.01 --to=2026.03.15\n")
	}
	fs.Parse(args)

	// Write to both stdout and store/sync.log
	logPath := filepath.Join(dataDir(), "sync.log")
	os.MkdirAll(filepath.Dir(logPath), 0755)
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening log file %s: %v\n", logPath, err)
		os.Exit(1)
	}
	defer logFile.Close()
	syncOut := io.MultiWriter(os.Stdout, logFile)

	earlyError := func(msg string) {
		fmt.Fprintf(syncOut, "[%s]\nERROR:\n%s\n", time.Now().Format("2006-01-02 15:04:05"), msg)
		os.Exit(1)
	}

	if !*catchup && !*deleteRange && *fromStr == "" {
		fs.Usage()
		os.Exit(1)
	}
	if *deleteRange && *fromStr == "" {
		earlyError("--delete requires --from")
	}

	store, err := NewMessageStore()
	if err != nil {
		earlyError(err.Error())
	}
	defer store.Close()

	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		earlyError(err.Error())
	}

	var fromTime, toTime time.Time

	if *toStr != "" {
		toTime, err = parseArchiveDate(*toStr)
		if err != nil {
			earlyError(err.Error())
		}
		if !hasTimePart(*toStr) {
			toTime = toTime.Add(23*time.Hour + 59*time.Minute + 59*time.Second)
		}
	} else {
		toTime = time.Now()
	}

	if *catchup {
		fromTime = latestArchivedTimestamp(*outputDir)
		if fromTime.IsZero() {
			fmt.Fprintf(syncOut, "[%s]\nNo existing archive files found. Nothing to catch up on (0 chats).\n", time.Now().Format("2006-01-02 15:04:05"))
			return
		}
	} else {
		fromTime, err = parseArchiveDate(*fromStr)
		if err != nil {
			earlyError(err.Error())
		}
	}

	fromISO := fromTime.Format(time.RFC3339)
	toISO := toTime.Format(time.RFC3339)

	dtFmt := "2006-01-02 15:04:05"
	ts := time.Now().Format(dtFmt)
	rangeFrom := fromTime.Format(dtFmt)
	rangeTo := toTime.Format(dtFmt)

	syncFailed := func(msg string) {
		fmt.Fprintf(syncOut, "[%s]\nRANGE: %s --> %s\nERROR:\n%s\n", ts, rangeFrom, rangeTo, msg)
		os.Exit(1)
	}

	if *deleteRange {
		cleanseAllFiles(*outputDir, fromTime, toTime)
		deleteEmptyArchives(*outputDir)
		fmt.Fprintf(syncOut, "[%s]\nRANGE: %s --> %s\nDELETED\n", ts, rangeFrom, rangeTo)
		return
	}

	// If the message database is empty, connect to WhatsApp and pull history
	var msgCount int
	store.db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&msgCount)
	if msgCount == 0 {
		fmt.Fprintf(syncOut, "No messages in database. Connecting to WhatsApp to pull history...\n")
		if err := populateFromWhatsApp(store, syncOut); err != nil {
			syncFailed(fmt.Sprintf("Failed to pull history from WhatsApp: %v", err))
		}
	}

	refreshGroupData(store, syncOut)

	enhanceAllFiles(store, *outputDir)
	cleanseAllFiles(*outputDir, fromTime, toTime)

	chats, err := getChatsInRange(store, fromISO, toISO)
	if err != nil {
		syncFailed(err.Error())
	}

	for _, chat := range chats {
		if err := archiveChat(store, chat, fromISO, toISO, *outputDir, fromTime, toTime); err != nil {
			syncFailed(err.Error())
		}
	}

	deleteEmptyArchives(*outputDir)

	fmt.Fprintf(syncOut, "[%s]\nRANGE: %s --> %s\nCHATS: %d\n", ts, rangeFrom, rangeTo, len(chats))
}

// deleteEmptyArchives removes .txt files in outputDir that have no message lines.
func deleteEmptyArchives(outputDir string) {
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}
		fp := filepath.Join(outputDir, entry.Name())
		if countMessageLines(fp) == 0 {
			os.Remove(fp)
			fmt.Printf("  Deleted (empty): %s\n", entry.Name())
		}
	}
}

func countMessageLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if _, ok := parseMessageTimestamp(scanner.Text()); ok {
			count++
		}
	}
	return count
}

// latestArchivedTimestamp scans all .txt files in outputDir and returns the
// latest message timestamp found. Returns zero time if no messages exist.
func latestArchivedTimestamp(outputDir string) time.Time {
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return time.Time{}
	}

	var latest time.Time
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}
		fp := filepath.Join(outputDir, entry.Name())
		f, err := os.Open(fp)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			if ts, ok := parseMessageTimestamp(scanner.Text()); ok && ts.After(latest) {
				latest = ts
			}
		}
		f.Close()
	}
	return latest
}

func getChatsInRange(store *MessageStore, fromISO, toISO string) ([]archivedChat, error) {
	rows, err := store.db.Query(`
		SELECT DISTINCT messages.chat_jid, chats.name, COUNT(*) as message_count
		FROM messages
		JOIN chats ON messages.chat_jid = chats.jid
		WHERE messages.timestamp >= ? AND messages.timestamp <= ?
		GROUP BY messages.chat_jid
		ORDER BY message_count DESC
	`, fromISO, toISO)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chats []archivedChat
	for rows.Next() {
		var c archivedChat
		var dbName *string
		if err := rows.Scan(&c.jid, &dbName, &c.msgCount); err != nil {
			return nil, err
		}

		if dbName != nil && *dbName != "" {
			c.name = *dbName
		} else {
			c.name = c.jid
		}

		if looksLikePhoneNumber(c.name) {
			resolved := store.GetSenderName(c.jid)
			if resolved != c.jid && !looksLikePhoneNumber(resolved) {
				c.name = resolved
			}
		}

		chats = append(chats, c)
	}
	return chats, nil
}

func archiveChat(store *MessageStore, chat archivedChat, fromISO, toISO, outputDir string, fromTime, toTime time.Time) error {
	// Query new messages from DB
	rows, err := store.db.Query(`
		SELECT messages.timestamp, messages.sender, messages.content, messages.is_from_me, messages.media_type
		FROM messages
		WHERE messages.chat_jid = ? AND messages.timestamp >= ? AND messages.timestamp <= ?
		ORDER BY messages.timestamp ASC
	`, chat.jid, fromISO, toISO)
	if err != nil {
		return fmt.Errorf("query messages: %v", err)
	}
	defer rows.Close()

	// Format new messages from DB into lines
	var newMessages []messageLine
	participantSet := make(map[string]struct{})
	participantSet[ownerName] = struct{}{}

	for rows.Next() {
		var timestamp, sender, content string
		var isFromMe bool
		var mediaType *string
		if err := rows.Scan(&timestamp, &sender, &content, &isFromMe, &mediaType); err != nil {
			return fmt.Errorf("scan message: %v", err)
		}

		ts, tsFormatted := parseDBTimestamp(timestamp)

		senderName := ownerName
		if !isFromMe {
			if store.isGroupSender(sender) {
				senderName = "(group member)"
			} else {
				senderName = store.GetSenderName(sender)
			}
			participantSet[sender] = struct{}{}
		}

		if mediaType != nil && *mediaType != "" {
			content = "media"
		}

		line := fmt.Sprintf("[%s] %s: %s", tsFormatted, senderName, content)
		newMessages = append(newMessages, messageLine{ts: ts, line: line})
	}

	// Find existing file and read retained messages (outside the window)
	filename := jidToFilename(chat.jid)
	renameStaleFile(outputDir, chat.jid, filename)
	fp := filepath.Join(outputDir, filename)
	retained := readRetainedMessages(fp, fromTime, toTime)

	// Merge retained + new, sort chronologically
	allMessages := append(retained, newMessages...)
	if len(allMessages) == 0 {
		return nil
	}
	sort.Slice(allMessages, func(i, j int) bool { return allMessages[i].ts.Before(allMessages[j].ts) })

	// Expand group-JID participants to actual members.
	// When history sync records the group itself as the sender, replace the
	// group entry with the individual members from the group_participants table.
	expandedSet := make(map[string]struct{})
	for p := range participantSet {
		if store.isGroupSender(p) {
			groupJID := p + "@g.us"
			members := store.GetGroupParticipants(groupJID)
			for _, m := range members {
				expandedSet[m] = struct{}{}
			}
		} else {
			expandedSet[p] = struct{}{}
		}
	}
	expandedSet[ownerName] = struct{}{}

	participants := make([]string, 0, len(expandedSet))
	for p := range expandedSet {
		participants = append(participants, p)
	}
	sort.Strings(participants)

	// Write the file
	f, err := os.Create(fp)
	if err != nil {
		return fmt.Errorf("create file: %v", err)
	}
	defer f.Close()

	fmt.Fprintln(f, strings.Repeat("=", 80))
	fmt.Fprintf(f, "CHAT: %s\n", chat.name)
	fmt.Fprintf(f, "CHAT JID: %s\n", chat.jid)
	fmt.Fprintln(f, strings.Repeat("-", 80))
	fmt.Fprintln(f, "PARTICIPANTS:")
	ownerPrinted := false
	for _, p := range participants {
		if p == ownerName {
			if !ownerPrinted {
				fmt.Fprintf(f, "  - %s\n", ownerName)
				ownerPrinted = true
			}
			continue
		}
		info := store.ResolveParticipant(p)
		if info.Name == ownerName {
			if !ownerPrinted {
				fmt.Fprintf(f, "  - %s\n", ownerName)
				ownerPrinted = true
			}
			continue
		}
		fmt.Fprintln(f, FormatParticipantLine(info))
	}
	fmt.Fprintln(f, strings.Repeat("=", 80))
	fmt.Fprintln(f)

	for _, m := range allMessages {
		fmt.Fprintln(f, m.line)
	}

	return nil
}

func parseDBTimestamp(timestamp string) (time.Time, string) {
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05",
	}
	for _, fmt := range formats {
		if t, err := time.Parse(fmt, timestamp); err == nil {
			return t, t.Format("2006-01-02 15:04:05")
		}
	}
	return time.Time{}, timestamp
}
