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

func SSERead(reader io.Reader, handler func(b []byte) error) error {

	chEve := make(chan string)
	chErr := make(chan error)

	scanner := bufio.NewScanner(reader)
	initBufferSize := 1024
	maxBufferSize := 4096
	scanner.Buffer(make([]byte, initBufferSize), maxBufferSize)
	split := func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		lennbefore := bytes.Index(data, []byte("\n\n"))
		if lennbefore >= 0 {
			return lennbefore + 2, data[0:lennbefore], nil
		}
		if atEOF {
			return len(data), data, nil
		}
		return 0, nil, nil
	}
	scanner.Split(split)

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
		case msg := <-chEve:
			if err := handler([]byte(msg + "\n")); err != nil {
				return err
			}
		}
	}
}

func (c *ChatGPT) Chat(input string) (string, error) {
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
		return "", fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequest(
		http.MethodPost,
		"https://api.openai.com/v1/chat/completions",
		bytes.NewBuffer([]byte(b)),
	)
	if err != nil {
		return "", err
	}
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apikey)

	res, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer res.Body.Close()

	header := "data:"
	h := func(b []byte) error {
		w := os.Stdout
		if strings.HasPrefix(string(b), header) {
			b = []byte(strings.TrimPrefix(string(b), header))
		}
		if string(b) == " [DONE]\n" { // TODO: trim space and \n
			return io.EOF
		}
		if _, err := w.Write(b); err != nil {
			return err
		}
		return nil
	}

	if err := SSERead(res.Body, h); err != nil {
		return "", fmt.Errorf("failed to read SSE: %w", err)
	}

	return "", nil
}

func main() {
	API_KEY, ok := os.LookupEnv("CHATGPT_API_SECRET")
	if !ok {
		log.Fatalf("CHATGPT_API_SECRET not set")
	}

	client := http.Client{}
	c := NewChatGPT(API_KEY, &client)
	c.Chat("こんにちは")

}
