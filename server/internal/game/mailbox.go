// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"

	"tidelab/internal/common"
	"tidelab/internal/gateway"
	"tidelab/internal/save"
	"tidelab/protocol"
)

// The file-backed mailbox (R-55, R-102; specs/activities.md §5, save-format.md §7). Each mail is
// a text file mailbox/<player id>/<mail id>.mail under the state directory:
//
//	title: 守岸人的武器
//	sender: Tidelab
//	content: 第一行
//	content: 第二行
//	attach: 21050036 5        (shown in the mail; display only)
//	cmd: weapon 21050036 5    (run as a chat command when the mail is claimed)
//
// Its state lives next to it in <mail id>.state as "<state bits> <received unix time>". State
// bits follow the client: 1 read, 2 attachments claimed, 4 favorite. A mail without a .state
// file has not been shown to the client yet. Ported from our C# Game/Mailbox.cs.

const (
	MailStateRead    = 1
	MailStateClaimed = 2
	// MailValidSeconds is R-52: 30 days, like the real game's mail.
	MailValidSeconds int64 = 30 * 24 * 3600
	utf8BOM                = string(rune(0xFEFF))
)

type Mail struct {
	ID          string
	Title       string
	Sender      string
	Content     string
	Attachments []*protocol.PbMailAttachment
	Cmds        []string
	State       int32
	Received    int64
	Announced   bool
}

func mailDir(stateDir string, playerID int32) string {
	return filepath.Join(stateDir, "mailbox", strconv.Itoa(int(playerID)))
}

func mailFile(stateDir string, playerID int32, id, ext string) string {
	return filepath.Join(mailDir(stateDir, playerID), id+ext)
}

func ValidMailID(id string) bool {
	if len(id) == 0 || len(id) > 96 || strings.Contains(id, "..") {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

// CleanMailLine is a .mail line without surrounding blanks and without a UTF-8 BOM in front
// (R-55: never cut a character's last byte).
func CleanMailLine(raw string) string {
	line := strings.Trim(raw, " \t\r")
	if strings.HasPrefix(line, utf8BOM) {
		line = strings.Trim(line[len(utf8BOM):], " \t\r")
	}
	return line
}

func ParseMail(id, text string) *Mail {
	m := &Mail{ID: id, Sender: "Tidelab"}
	var content strings.Builder
	for _, raw := range strings.Split(text, "\n") {
		line := CleanMailLine(raw)
		if line == "" || line[0] == '#' {
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		key := strings.Trim(line[:colon], " \t")
		value := strings.Trim(line[colon+1:], " \t")
		switch key {
		case "title":
			m.Title = value
		case "sender":
			m.Sender = value
		case "content":
			if content.Len() != 0 {
				content.WriteByte('\n')
			}
			content.WriteString(value)
		case "attach":
			parts := splitSpaces(value)
			if len(parts) == 0 {
				break
			}
			item, ok := parseSigned(parts[0], 32)
			if !ok {
				break
			}
			count := int64(1)
			if len(parts) > 1 {
				if c, ok := parseSigned(parts[1], 32); ok {
					count = c
				}
			}
			m.Attachments = append(m.Attachments, &protocol.PbMailAttachment{Id: int32(item), Count: int32(count)})
		case "cmd":
			if value != "" {
				m.Cmds = append(m.Cmds, value)
			}
		}
	}
	m.Content = content.String()
	return m
}

func LoadMail(stateDir string, playerID int32, id string) *Mail {
	if !ValidMailID(id) {
		return nil
	}
	b, err := os.ReadFile(mailFile(stateDir, playerID, id, ".mail"))
	if err != nil {
		return nil
	}
	m := ParseMail(id, strings.TrimPrefix(string(b), utf8BOM))
	if sb, err := os.ReadFile(mailFile(stateDir, playerID, id, ".state")); err == nil {
		parts := strings.FieldsFunc(string(sb), func(r rune) bool { return r == ' ' || r == '\t' || r == '\r' || r == '\n' })
		if len(parts) > 0 {
			if v, ok := parseSigned(parts[0], 32); ok {
				m.State = int32(v)
			}
		}
		if len(parts) > 1 {
			if v, ok := parseSigned(parts[1], 64); ok {
				m.Received = v
			}
		}
		m.Announced = true
	}
	return m
}

// ListMail is all mails of the player, oldest file name first (ordinal byte order).
func ListMail(stateDir string, playerID int32) []*Mail {
	entries, err := os.ReadDir(mailDir(stateDir, playerID))
	if err != nil {
		return nil
	}
	var ids []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".mail") {
			ids = append(ids, strings.TrimSuffix(e.Name(), ".mail"))
		}
	}
	sort.Strings(ids)
	var out []*Mail
	for _, id := range ids {
		if m := LoadMail(stateDir, playerID, id); m != nil {
			out = append(out, m)
		}
	}
	return out
}

func WriteMailState(stateDir string, playerID int32, m *Mail, state int32, now int64) {
	if m.Received == 0 {
		m.Received = now
	}
	m.State = state
	m.Announced = true
	save.WriteAtomic(mailFile(stateDir, playerID, m.ID, ".state"), fmt.Sprintf("%d %d\n", state, m.Received))
}

func RemoveMail(stateDir string, playerID int32, id string) bool {
	if !ValidMailID(id) {
		return false
	}
	os.Remove(mailFile(stateDir, playerID, id, ".mail"))
	os.Remove(mailFile(stateDir, playerID, id, ".state"))
	return true
}

func MailExpiryTime(m *Mail) int64 { return m.Received + MailValidSeconds }

func MailToPb(m *Mail) *protocol.PbMailInfo {
	var read int64
	if m.State&MailStateRead != 0 {
		read = m.Received
	}
	return &protocol.PbMailInfo{
		Id: m.ID, ReceivedTime: m.Received, ReadTime: read, State: m.State, Level: protocol.MailLevel_MailLevel_General,
		Title: m.Title, Content: m.Content, Sender: m.Sender, ValidTime: int32(MailValidSeconds),
		Attachments: m.Attachments, ExpiryTime: m.Received + MailValidSeconds,
	}
}

// ----- in the session (specs/activities.md §5) -----

func (s *PlayerSession) stateDir() string { return s.Files.StateDir }

// mailInfos is the login push (enter-world.md §3 row 29): every mail, the new ones marked shown now.
func (s *PlayerSession) mailInfos() *protocol.MailInfosNotify {
	n := &protocol.MailInfosNotify{}
	now := s.Clock.Seconds()
	for _, m := range ListMail(s.stateDir(), s.State.PlayerID()) {
		if !m.Announced {
			WriteMailState(s.stateDir(), s.State.PlayerID(), m, 0, now)
		}
		n.MailInfos = append(n.MailInfos, MailToPb(m))
	}
	return n
}

// AnnounceNewMail is live delivery (quests.md §5.3 step 4): mails dropped in since go out as MailAddNotify.
func (s *PlayerSession) AnnounceNewMail() {
	now := s.Clock.Seconds()
	for _, m := range ListMail(s.stateDir(), s.State.PlayerID()) {
		if m.Announced {
			continue
		}
		WriteMailState(s.stateDir(), s.State.PlayerID(), m, 0, now)
		s.Client.Push(&protocol.MailAddNotify{NewMail: MailToPb(m)})
		common.Info("mail", fmt.Sprintf("mail %s delivered: %s", m.ID, m.Title))
	}
}

func (s *PlayerSession) MailRead(r *protocol.MailReadRequest) *protocol.MailReadResponse {
	m := LoadMail(s.stateDir(), s.State.PlayerID(), r.Id)
	if m == nil {
		return &protocol.MailReadResponse{Id: r.Id, ErrorCode: protocol.ErrorCode_ErrorCode_RequestParamError}
	}
	now := s.Clock.Seconds()
	WriteMailState(s.stateDir(), s.State.PlayerID(), m, m.State|MailStateRead, now)
	return &protocol.MailReadResponse{Id: r.Id, State: m.State, ReadTime: now, ExpiryTime: MailExpiryTime(m), ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

// MailGetAttachment is R-102: the claimed state is saved before the cmd: lines run, so a
// failing command cannot be claimed twice.
func (s *PlayerSession) MailGetAttachment(r *protocol.MailGetAttachmentRequest) *protocol.MailGetAttachmentResponse {
	resp := &protocol.MailGetAttachmentResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, SuccessIdMap: map[string]int32{}}
	now := s.Clock.Seconds()
	run := s.RunCommand
	if run == nil {
		run = s.RunCommands
	}
	for _, id := range r.MailIds {
		m := LoadMail(s.stateDir(), s.State.PlayerID(), id)
		if m == nil || m.State&MailStateClaimed != 0 {
			continue
		}
		WriteMailState(s.stateDir(), s.State.PlayerID(), m, m.State|MailStateRead|MailStateClaimed, now)
		for _, cmd := range m.Cmds {
			ok := false
			if guard("mail", fmt.Sprintf("mail %s: command \"%s\"", id, cmd), func() { ok = run(cmd) }) {
				continue
			}
			if !ok {
				common.Warn("mail", fmt.Sprintf("mail %s: command \"%s\" failed", id, cmd))
			}
		}
		resp.SuccessIdMap[m.ID] = m.State
	}
	return resp
}

func (s *PlayerSession) MailDelete(r *protocol.MailDeleteRequest) *protocol.MailDeleteResponse {
	resp := &protocol.MailDeleteResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
	for _, id := range r.MailIds {
		if RemoveMail(s.stateDir(), s.State.PlayerID(), id) {
			resp.SuccessIds = append(resp.SuccessIds, id)
		}
	}
	return resp
}

func (g *WorldGame) registerMail(h *gateway.MessageHandlers) {
	gateway.OnRequest(h, func(*gateway.Session, *protocol.MailBindInfoRequest) proto.Message {
		return &protocol.MailBindInfoResponse{}
	})
	reqOn(h, g, func(p *PlayerSession, r *protocol.MailReadRequest) *protocol.MailReadResponse { return p.MailRead(r) },
		func(r *protocol.MailReadRequest) *protocol.MailReadResponse {
			return &protocol.MailReadResponse{Id: r.Id, ErrorCode: protocol.ErrorCode_ErrorCode_RequestParamError}
		})
	reqOn(h, g, func(p *PlayerSession, r *protocol.MailGetAttachmentRequest) *protocol.MailGetAttachmentResponse {
		return p.MailGetAttachment(r)
	},
		func(*protocol.MailGetAttachmentRequest) *protocol.MailGetAttachmentResponse {
			return &protocol.MailGetAttachmentResponse{}
		})
	reqOn(h, g, func(p *PlayerSession, r *protocol.MailDeleteRequest) *protocol.MailDeleteResponse {
		return p.MailDelete(r)
	},
		func(*protocol.MailDeleteRequest) *protocol.MailDeleteResponse { return &protocol.MailDeleteResponse{} })
}
