// Package photos provides the protocol boundary to a fixed read-only host broker.
package photos

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mattyv/nas-search-mcp/internal/policy"
)

var ErrInput = errors.New("invalid Photos input: use query up to 100 characters, offset 0–100000, limit 1–100, and positive person/photo IDs")
var ErrUnavailable = errors.New("Synology Photos is unavailable; check the host Photos broker")
var errReply = errors.New("Synology Photos returned an invalid or unsupported response")

type PeopleInput struct {
	Query  string `json:"query"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}
type PhotosInput struct {
	PersonID int64 `json:"person_id"`
	Offset   int   `json:"offset"`
	Limit    int   `json:"limit"`
}
type PreviewInput struct {
	PersonID int64 `json:"person_id"`
	PhotoID  int64 `json:"photo_id"`
}
type PreviewResult struct {
	MIMEType string
	Data     []byte
}
type Person struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	ItemCount int64  `json:"item_count"`
}
type Photo struct {
	ID        int64  `json:"id"`
	Filename  string `json:"filename"`
	TakenTime int64  `json:"taken_time"`
	Type      string `json:"type"`
}
type PeopleResult struct {
	People     []Person `json:"people"`
	Offset     int      `json:"offset"`
	NextOffset *int     `json:"next_offset"`
	Truncated  bool     `json:"truncated"`
}
type PhotosResult struct {
	Photos     []Photo `json:"photos"`
	Offset     int     `json:"offset"`
	NextOffset *int    `json:"next_offset"`
	Truncated  bool    `json:"truncated"`
}
type Client struct{ http *http.Client }

func (c *Client) Preview(ctx context.Context, in PreviewInput) (PreviewResult, error) {
	if in.PersonID < 1 || in.PersonID > 2147483647 || !policy.ValidPhotoPerson(int(in.PersonID)) || in.PhotoID < 1 || in.PhotoID > 2147483647 || !policy.ValidPhotoPerson(int(in.PhotoID)) {
		return PreviewResult{}, ErrInput
	}
	var reply struct {
		MIMEType string `json:"mime_type"`
		Data     string `json:"data"`
	}
	if err := c.post(ctx, "/preview", in, &reply); err != nil {
		return PreviewResult{}, err
	}
	if reply.MIMEType != "image/jpeg" || len(reply.Data) > base64.StdEncoding.EncodedLen(512*1024) || strings.ContainsAny(reply.Data, "\r\n\t ") {
		return PreviewResult{}, errReply
	}
	data, err := base64.StdEncoding.Strict().DecodeString(reply.Data)
	if err != nil || len(data) < 4 || len(data) > 512*1024 || data[0] != 0xff || data[1] != 0xd8 || data[len(data)-2] != 0xff || data[len(data)-1] != 0xd9 {
		return PreviewResult{}, errReply
	}
	return PreviewResult{MIMEType: reply.MIMEType, Data: data}, nil
}

func New(socket string) (*Client, error) {
	if !filepath.IsAbs(socket) || filepath.Clean(socket) != socket || len(socket) > 107 || strings.ContainsRune(socket, 0) || socket == "/" {
		return nil, errors.New("Photos socket must be an absolute normalized Unix socket path of at most 107 bytes")
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socket)
		},
		MaxConnsPerHost: 2, MaxIdleConns: 1, MaxIdleConnsPerHost: 1, IdleConnTimeout: 30 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	return &Client{http: &http.Client{Transport: transport, Timeout: 45 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func page(offset, limit int) (int, error) {
	if limit == 0 {
		limit = 20
	}
	if !policy.ValidPhotoPage(offset, limit) {
		return 0, ErrInput
	}
	return limit, nil
}

func boundedText(value string, max int) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > max {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validPage(offset int, next *int, requested, count, limit int) bool {
	return offset == requested && count <= limit && (next == nil || (*next > offset && policy.ValidPhotoPage(*next, 1)))
}

func (c *Client) post(ctx context.Context, path string, in, out any) error {
	if c == nil || c.http == nil {
		return ErrUnavailable
	}
	body, err := json.Marshal(in)
	if err != nil {
		return ErrInput
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://synology-photos"+path, bytes.NewReader(body))
	if err != nil {
		return ErrInput
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024+1))
	if err != nil || len(data) > 1024*1024 {
		return errReply
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil {
		return errReply
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return errReply
	}
	return nil
}

func (c *Client) People(ctx context.Context, in PeopleInput) (PeopleResult, error) {
	var out PeopleResult
	limit, err := page(in.Offset, in.Limit)
	if err != nil || !boundedText(in.Query, 100) {
		return out, ErrInput
	}
	in.Limit = limit
	if err = c.post(ctx, "/people", in, &out); err != nil {
		return PeopleResult{}, err
	}
	if out.People == nil || !validPage(out.Offset, out.NextOffset, in.Offset, len(out.People), limit) {
		return PeopleResult{}, errReply
	}
	for _, person := range out.People {
		if person.ID <= 0 || person.ID > 2147483647 || !policy.ValidPhotoPerson(int(person.ID)) || person.ItemCount < 0 || !boundedText(person.Name, 256) {
			return PeopleResult{}, errReply
		}
	}
	return out, nil
}

func (c *Client) ForPerson(ctx context.Context, in PhotosInput) (PhotosResult, error) {
	var out PhotosResult
	limit, err := page(in.Offset, in.Limit)
	if err != nil || in.PersonID < 1 || in.PersonID > 2147483647 || !policy.ValidPhotoPerson(int(in.PersonID)) {
		return out, ErrInput
	}
	in.Limit = limit
	if err = c.post(ctx, "/photos", in, &out); err != nil {
		return PhotosResult{}, err
	}
	if out.Photos == nil || !validPage(out.Offset, out.NextOffset, in.Offset, len(out.Photos), limit) {
		return PhotosResult{}, errReply
	}
	for _, photo := range out.Photos {
		if photo.ID <= 0 || photo.ID > 2147483647 || !policy.ValidPhotoPerson(int(photo.ID)) || photo.Filename == "" || !boundedText(photo.Filename, 1024) || strings.ContainsAny(photo.Filename, "/\\") || (photo.Type != "photo" && photo.Type != "video") {
			return PhotosResult{}, errReply
		}
	}
	return out, nil
}
