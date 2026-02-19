package handler

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/gocarina/gocsv"
	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"go.uber.org/zap"
)

// slackLinkRe matches Slack link markup: <url|label> or <url>
var slackLinkRe = regexp.MustCompile(`<([^|>]+)\|?[^>]*>`)

// SavedItemRow is the CSV output row for a saved item.
type SavedItemRow struct {
	Channel     string `csv:"channel"`
	ChannelName string `csv:"channel_name"`
	Ts          string `csv:"ts"`
	State       string `csv:"state"`
	DateSaved   string `csv:"date_saved"`
	DateDue     string `csv:"date_due"`
	User        string `csv:"user"`
	Text        string `csv:"text"`
	Link        string `csv:"link"`
	Cursor      string `csv:"cursor"`
}

type SavedHandler struct {
	apiProvider *provider.ApiProvider
	logger      *zap.Logger
}

func NewSavedHandler(apiProvider *provider.ApiProvider, logger *zap.Logger) *SavedHandler {
	return &SavedHandler{
		apiProvider: apiProvider,
		logger:      logger,
	}
}

func (h *SavedHandler) SavedListHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("SavedListHandler called", zap.Any("params", request.Params))

	cursor := request.GetString("cursor", "")

	// Fetch all pages of saved items transparently
	var allSavedItems []provider.SavedItem
	for {
		result, err := h.apiProvider.Slack().SavedListContext(ctx, cursor)
		if err != nil {
			h.logger.Error("SavedListContext failed", zap.Error(err))
			return nil, err
		}
		h.logger.Debug("Fetched saved items page", zap.Int("count", len(result.SavedItems)))
		allSavedItems = append(allSavedItems, result.SavedItems...)
		if result.ResponseMetadata.NextCursor == "" {
			break
		}
		cursor = result.ResponseMetadata.NextCursor
	}
	h.logger.Debug("Fetched all saved items", zap.Int("total_count", len(allSavedItems)))

	// Get workspace URL for building permalinks
	workspaceURL := ""
	if authResp, err := h.apiProvider.Slack().AuthTest(); err == nil {
		workspaceURL = strings.TrimRight(authResp.URL, "/")
	}

	// Resolve channel names from cache (best-effort)
	channelsCache := h.apiProvider.ProvideChannelsMaps()
	usersCache := h.apiProvider.ProvideUsersMap()

	var rows []SavedItemRow
	for _, item := range allSavedItems {
		channelName := ""
		if channelsCache != nil {
			if ch, ok := channelsCache.Channels[item.ItemID]; ok {
				channelName = ch.Name
			}
		}

		dateSaved := ""
		if item.DateCreated > 0 {
			dateSaved = time.Unix(item.DateCreated, 0).UTC().Format(time.RFC3339)
		}

		dateDue := ""
		if item.DateDue > 0 {
			dateDue = time.Unix(item.DateDue, 0).UTC().Format(time.RFC3339)
		}

		// Fetch the actual message text
		msgUser, msgText := h.fetchMessageText(ctx, item.ItemID, item.Ts, usersCache)

		// Build permalink: https://workspace.slack.com/archives/{channel}/p{ts_without_dot}
		link := ""
		if workspaceURL != "" && item.Ts != "" {
			link = workspaceURL + "/archives/" + item.ItemID + "/p" + strings.ReplaceAll(item.Ts, ".", "")
		}

		rows = append(rows, SavedItemRow{
			Channel:     item.ItemID,
			ChannelName: channelName,
			Ts:          item.Ts,
			State:       item.State,
			DateSaved:   dateSaved,
			DateDue:     dateDue,
			User:        msgUser,
			Text:        msgText,
			Link:        link,
		})
	}

	csvBytes, err := gocsv.MarshalBytes(&rows)
	if err != nil {
		h.logger.Error("Failed to marshal saved items to CSV", zap.Error(err))
		return nil, err
	}

	return mcp.NewToolResultText(string(csvBytes)), nil
}

func (h *SavedHandler) SavedCompleteHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("SavedCompleteHandler called", zap.Any("params", request.Params))

	channel := request.GetString("channel", "")
	if channel == "" {
		return nil, fmt.Errorf("channel is required")
	}

	ts := request.GetString("ts", "")
	if ts == "" {
		return nil, fmt.Errorf("ts is required")
	}

	if err := h.apiProvider.Slack().SavedCompleteContext(ctx, channel, ts); err != nil {
		h.logger.Error("SavedCompleteContext failed", zap.Error(err))
		return nil, err
	}

	return mcp.NewToolResultText("Item marked as complete."), nil
}

// fetchMessageText retrieves a single message by channel + ts and returns (username, text).
func (h *SavedHandler) fetchMessageText(ctx context.Context, channelID, ts string, usersCache *provider.UsersCache) (string, string) {
	params := &slack.GetConversationHistoryParameters{
		ChannelID: channelID,
		Latest:    ts,
		Oldest:    ts,
		Limit:     1,
		Inclusive:  true,
	}

	history, err := h.apiProvider.Slack().GetConversationHistoryContext(ctx, params)
	if err != nil {
		h.logger.Debug("Failed to fetch saved message", zap.String("channel", channelID), zap.String("ts", ts), zap.Error(err))
		return "", ""
	}

	if len(history.Messages) == 0 {
		return "", ""
	}

	msg := history.Messages[0]

	// Resolve user name
	userName := msg.User
	if usersCache != nil {
		if u, ok := usersCache.Users[msg.User]; ok {
			userName = u.RealName
		}
	}

	// Convert Slack link markup <url|label> or <url> to plain URLs
	text := slackLinkRe.ReplaceAllString(msg.Text, "$1")
	text = strings.ReplaceAll(text, "\n", " ")

	return userName, text
}
