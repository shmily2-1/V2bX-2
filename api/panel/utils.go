package panel

import (
	"fmt"
	"github.com/go-resty/resty/v2"
	"net/url"
	path2 "path"
	"strings"
)

// Debug set the client debug for client
func (c *Client) Debug() {
	c.client.SetDebug(true)
}

func (c *Client) assembleURL(path string) string {
	return path2.Join(c.APIHost + path)
}
func (c *Client) checkResponse(res *resty.Response, path string, err error) error {
	if err != nil {
		return fmt.Errorf("request %s failed: %s", c.assembleURL(path), redactError(err.Error(), c.Token))
	}
	if res == nil {
		return fmt.Errorf("request %s failed: missing response", c.assembleURL(path))
	}
	if res.StatusCode() >= 400 {
		body := res.Body()
		return fmt.Errorf("request %s failed: %s", c.assembleURL(path), redactError(string(body), c.Token))
	}
	return nil
}

func redactError(message, token string) string {
	if token == "" {
		return message
	}
	for _, value := range []string{url.QueryEscape(token), url.PathEscape(token), token} {
		message = strings.ReplaceAll(message, value, "[redacted]")
	}
	return message
}
