// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"fmt"
	"os"
	"strings"

	"google.golang.org/protobuf/proto"

	"tidelab/internal/common"
	"tidelab/internal/gateway"
	"tidelab/protocol"
)

// The command bot (specs/systems.md §8): a fixed friend the player chats with to run commands
// (§12), and the dev command file polled at the heartbeat (§7.1). Ported from our C#
// Game/PlayerSession.Chat.cs.

const (
	BotID       = 1337
	BotGreeting = "use \"help\" to get a list of commands."
)

func BotDetails() *protocol.PlayerDetails {
	return &protocol.PlayerDetails{
		PlayerId: BotID, Name: "Tidelab", Level: 90, OriginWorldLevel: 8, CurWorldLevel: 8,
		HeadId: 82001412, HeadFrameId: 0, Signature: "here to help!",
		IsOnline: true, IsCanLobbyOnline: false, MatchPsnUser: true,
	}
}

func botText(text string) *protocol.ChatContentProto {
	return &protocol.ChatContentProto{SenderUID: BotID, ChatContentType: protocol.ChatContentType_ChatContentType_Text, Content: text, OfflineMsg: false, UTCTime: 0}
}

// BotSay is a reply of the bot.
func (s *PlayerSession) BotSay(text string) {
	s.Client.Push(&protocol.PrivateMessageNotify{ChatContent: botText(text)})
}

// RunCommands runs commands (§12.1) and has the bot say every reply; after the current request's
// reply, a transfer they asked for and a spawn refresh if they woke something. False when one failed.
func (s *PlayerSession) RunCommands(input string) bool {
	var replies []string
	ok := func() (ok bool) {
		defer func() {
			if r := recover(); r != nil {
				common.Warn("command", fmt.Sprintf("\"%s\" failed: %v", input, r))
				replies = append(replies, fmt.Sprintf("\"%s\" failed: %v", input, r))
				ok = false
			}
		}()
		return RunCommandLine(s, input, &replies)
	}()
	for _, r := range replies {
		s.BotSay(r)
	}
	s.After(func() {
		s.ApplyPendingTransfer()
		s.ImmediateSpawnCheck()
	})
	return ok
}

// pollCommandFile is §12 item 3: a non-blank command file is emptied, then each of its lines runs.
func (s *PlayerSession) pollCommandFile() {
	path := s.CommandFile
	if path == "" || !common.FileExists(path) {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		common.Warn("command", fmt.Sprintf("%s: %v", path, err))
		return
	}
	text := strings.TrimPrefix(string(b), utf8BOM)
	if strings.TrimFunc(text, isNetSpace) == "" {
		return
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		common.Warn("command", fmt.Sprintf("%s: %v", path, err))
		return
	}
	for _, line := range strings.FieldsFunc(text, func(r rune) bool { return r == '\r' || r == '\n' }) {
		cmd := strings.Trim(line, " \t")
		if cmd == "" {
			continue
		}
		common.Info("command", "cmd.txt: "+cmd)
		s.RunCommands(cmd)
	}
	s.RunAfterResponse()
}

func (s *PlayerSession) PrivateChatData() *protocol.PrivateChatDataResponse {
	s.Client.Push(&protocol.PrivateChatHistoryNotify{AllChats: []*protocol.PrivateChatHistoryContentProto{{
		TargetUID: BotID, Chats: []*protocol.ChatContentProto{botText(BotGreeting)}, HistoryIsEnd: false, TotalNums: 1,
	}}})
	return &protocol.PrivateChatDataResponse{LoadSucc: true}
}

func (s *PlayerSession) PrivateChatHistory(r *protocol.PrivateChatHistoryRequest) *protocol.PrivateChatHistoryResponse {
	if r.TargetUID == BotID {
		s.BotSay(BotGreeting)
	}
	return &protocol.PrivateChatHistoryResponse{}
}

func (s *PlayerSession) PrivateChat(r *protocol.PrivateChatRequest) *protocol.PrivateChatResponse {
	common.Info("command", "chat: "+r.Content)
	s.RunCommands(r.Content)
	return &protocol.PrivateChatResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, TargetUID: r.TargetUID, FilterMsg: r.Content}
}

func (g *WorldGame) registerChat(h *gateway.MessageHandlers) {
	ok := protocol.ErrorCode_ErrorCode_Success
	reqOn(h, g, func(p *PlayerSession, _ *protocol.PrivateChatDataRequest) *protocol.PrivateChatDataResponse {
		return p.PrivateChatData()
	},
		func(*protocol.PrivateChatDataRequest) *protocol.PrivateChatDataResponse {
			return &protocol.PrivateChatDataResponse{}
		})
	reqOn(h, g, func(p *PlayerSession, r *protocol.PrivateChatHistoryRequest) *protocol.PrivateChatHistoryResponse {
		return p.PrivateChatHistory(r)
	},
		func(*protocol.PrivateChatHistoryRequest) *protocol.PrivateChatHistoryResponse {
			return &protocol.PrivateChatHistoryResponse{}
		})
	reqOn(h, g, func(p *PlayerSession, r *protocol.PrivateChatRequest) *protocol.PrivateChatResponse {
		return p.PrivateChat(r)
	},
		func(*protocol.PrivateChatRequest) *protocol.PrivateChatResponse {
			return &protocol.PrivateChatResponse{ErrorCode: protocol.ErrorCode_ErrorCode_RequestParamError}
		})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.PrivateChatOperateRequest) proto.Message {
		return &protocol.PrivateChatOperateResponse{ErrorCode: ok}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.FriendAllRequest) proto.Message {
		return &protocol.FriendAllResponse{ErrorCode: ok, FriendInfoList: []*protocol.FriendInfo{{Info: BotDetails(), Remark: ""}}}
	})
	// For any other id, answer RequestParamError (§8.3) so the client never hangs waiting.
	gateway.OnRequest(h, func(_ *gateway.Session, r *protocol.PlayerBasicInfoGetRequest) proto.Message {
		if r.Id == BotID {
			return &protocol.PlayerBasicInfoGetResponse{ErrorCode: ok, Info: BotDetails()}
		}
		return &protocol.PlayerBasicInfoGetResponse{ErrorCode: protocol.ErrorCode_ErrorCode_RequestParamError}
	})
}
