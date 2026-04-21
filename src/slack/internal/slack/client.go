package slack

import (
	"fmt"

	"github.com/slack-go/slack"
)

// Client wraps the Slack API client with token management
type Client struct {
	tokenManager *TokenManager
	api          *slack.Client
}

// NewClient creates a new Slack client with token refresh support
func NewClient(accessToken, refreshToken, clientID, clientSecret string) *Client {
	tm := NewTokenManager(accessToken, refreshToken, clientID, clientSecret)
	api := slack.New(accessToken)

	return &Client{
		tokenManager: tm,
		api:          api,
	}
}

// ensureValidToken ensures we have a fresh token and updates the API client if needed
func (c *Client) ensureValidToken() error {
	token, err := c.tokenManager.GetAccessToken()
	if err != nil {
		return err
	}

	// Update API client with fresh token
	c.api = slack.New(token)
	return nil
}

// GetConversations lists all channels the user is in
func (c *Client) GetConversations() ([]slack.Channel, error) {
	if err := c.ensureValidToken(); err != nil {
		return nil, err
	}

	params := &slack.GetConversationsParameters{
		ExcludeArchived: true,
		Types:           []string{"public_channel", "private_channel", "mpim", "im"},
		Limit:           200,
	}

	var allChannels []slack.Channel
	for {
		channels, nextCursor, err := c.api.GetConversations(params)
		if err != nil {
			return nil, fmt.Errorf("get conversations: %w", err)
		}

		allChannels = append(allChannels, channels...)

		if nextCursor == "" {
			break
		}
		params.Cursor = nextCursor
	}

	return allChannels, nil
}

// GetConversationHistory gets messages from a channel
func (c *Client) GetConversationHistory(channelID string, limit int) (*slack.GetConversationHistoryResponse, error) {
	if err := c.ensureValidToken(); err != nil {
		return nil, err
	}

	params := &slack.GetConversationHistoryParameters{
		ChannelID: channelID,
		Limit:     limit,
	}

	return c.api.GetConversationHistory(params)
}

// GetConversationReplies gets replies in a thread
func (c *Client) GetConversationReplies(channelID, threadTS string) ([]slack.Message, error) {
	if err := c.ensureValidToken(); err != nil {
		return nil, err
	}

	params := &slack.GetConversationRepliesParameters{
		ChannelID: channelID,
		Timestamp: threadTS,
	}

	msgs, _, _, err := c.api.GetConversationReplies(params)
	return msgs, err
}

// GetUserInfo gets information about a user
func (c *Client) GetUserInfo(userID string) (*slack.User, error) {
	if err := c.ensureValidToken(); err != nil {
		return nil, err
	}

	return c.api.GetUserInfo(userID)
}

// GetPermalink gets a permalink for a message
func (c *Client) GetPermalink(channelID, messageTS string) (string, error) {
	if err := c.ensureValidToken(); err != nil {
		return "", err
	}

	params := &slack.PermalinkParameters{
		Channel: channelID,
		Ts:      messageTS,
	}

	return c.api.GetPermalink(params)
}

// SearchMessages searches for messages
func (c *Client) SearchMessages(query string, limit int) (*slack.SearchMessages, error) {
	if err := c.ensureValidToken(); err != nil {
		return nil, err
	}

	params := slack.SearchParameters{
		Count: limit,
	}

	return c.api.SearchMessages(query, params)
}

// GetChannelInfo gets information about a channel
func (c *Client) GetChannelInfo(channelID string) (*slack.Channel, error) {
	if err := c.ensureValidToken(); err != nil {
		return nil, err
	}

	return c.api.GetConversationInfo(&slack.GetConversationInfoInput{
		ChannelID: channelID,
	})
}
