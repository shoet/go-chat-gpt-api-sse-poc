package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

type Client interface {
	Do(req *http.Request) (*http.Response, error)
}

type ChatGPT struct {
	apikey string
	client Client
}

func NewChatGPT(apikey string, client *http.Client) *ChatGPT {
	return &ChatGPT{apikey: apikey, client: client}
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func SplitScanner(r io.Reader, sep string) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	initBufferSize := 1024
	maxBufferSize := 4096
	scanner.Buffer(make([]byte, initBufferSize), maxBufferSize)
	split := func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		lennbefore := bytes.Index(data, []byte(sep))
		if lennbefore >= 0 {
			return lennbefore + len(sep), data[0:lennbefore], nil
		}
		if atEOF {
			return len(data), data, nil
		}
		return 0, nil, nil
	}
	scanner.Split(split)
	return scanner
}

func SSERead(reader io.Reader, handler func(b []byte) error) error {
	chEve := make(chan string)
	chErr := make(chan error)
	scanner := SplitScanner(reader, "\n\n")
	go func() {
		for scanner.Scan() {
			b := scanner.Bytes()
			chEve <- string(b)
		}
		if err := scanner.Err(); err != nil {
			chErr <- err
			return
		}
		chErr <- io.EOF
	}()

	for {
		select {
		case err := <-chErr:
			if err == io.EOF {
				return nil
			}
			return err
		case event := <-chEve:
			if err := handler([]byte(event + "\n")); err != nil {
				return err
			}
		}
	}
}

type ChatGPTResponseDelta struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatGPTRequestChoice struct {
	Index        int                  `json:"index"`
	Delta        ChatGPTResponseDelta `json:"delta"`
	FinishReason string               `json:"finish_reason"`
}

type ChatGPTResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Created int64                  `json:"created"`
	Model   string                 `json:"model"`
	Choices []ChatGPTRequestChoice `json:"choices"`
}

func (c *ChatGPT) Chat(input string) ([]byte, error) {
	messages := []message{
		{
			Role:    "user",
			Content: input,
		},
	}

	requestBody := struct {
		Model    string    `json:"model"`
		Messages []message `json:"messages"`
		Stream   bool      `json:"stream"`
	}{
		Model:    "gpt-3.5-turbo",
		Messages: messages,
		Stream:   true,
	}

	b, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequest(
		http.MethodPost,
		"https://api.openai.com/v1/chat/completions",
		bytes.NewBuffer([]byte(b)),
	)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apikey)

	res, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer res.Body.Close()

	header := "data:"
	var buffer []byte
	handler := func(b []byte) error {
		w := os.Stdout
		if strings.HasPrefix(string(b), header) {
			b = []byte(strings.TrimPrefix(string(b), header))
		}
		b = []byte(strings.TrimSpace(string(b)))
		if string(b) == "[DONE]" {
			return nil
		}

		var resp ChatGPTResponse
		if err := json.Unmarshal(b, &resp); err != nil {
			return fmt.Errorf("failed to unmarshal response: %w", err)
		}

		wb := []byte(resp.Choices[0].Delta.Content)
		if _, err := w.Write(wb); err != nil {
			return err
		}
		buffer = append(buffer, wb...)
		return nil
	}

	if err := SSERead(res.Body, handler); err != nil {
		return nil, fmt.Errorf("failed to read SSE: %w", err)
	}

	return buffer, nil
}

func main() {
	API_KEY, ok := os.LookupEnv("CHATGPT_API_SECRET")
	if !ok {
		log.Fatalf("CHATGPT_API_SECRET not set")
	}

	client := http.Client{}
	c := NewChatGPT(API_KEY, &client)
	tk, err := c.Chat(os.Args[1])
	if err != nil {
		log.Fatalf("failed to chat: %v", err)
	}

	_ = tk
}
