package streamlatency

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type firstByteRoundTripFunc func(*http.Request) (*http.Response, error)

func (f firstByteRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFirstByteIncludesHeadersAndFreezesOnPartialRead(t *testing.T) {
	base := &http.Client{Transport: firstByteRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		time.Sleep(30 * time.Millisecond)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("event: response.created\n\n")), Request: req}, nil
	})}
	client := TrackFirstByteClient(base)
	req := EnableFirstByte(httptest.NewRequest("POST", "http://example.com/v1/responses", nil))
	req.RequestURI = ""
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	ms, tracked := FirstByteMilliseconds(resp)
	require.True(t, tracked)
	require.Nil(t, ms, "headers alone are not a body byte")
	n, err := resp.Body.Read(make([]byte, 1))
	require.NoError(t, err)
	require.Equal(t, 1, n)
	first, _ := FirstByteMilliseconds(resp)
	require.NotNil(t, first)
	require.GreaterOrEqual(t, *first, 30)
	time.Sleep(25 * time.Millisecond)
	rest, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "vent: response.created\n\n", string(rest))
	last, _ := FirstByteMilliseconds(resp)
	require.Equal(t, *first, *last)
	require.NotEqual(t, base, client)
	_, wrapped := base.Transport.(firstByteTransport)
	require.False(t, wrapped, "shared transport must remain unchanged")
}

type firstByteScriptedBody struct {
	reads    int
	closed   bool
	withData bool
}

func (b *firstByteScriptedBody) Read(p []byte) (int, error) {
	b.reads++
	if b.reads == 1 {
		return 0, nil
	}
	if b.withData && b.reads == 2 {
		p[0] = ':'
		return 1, io.EOF
	}
	return 0, io.EOF
}
func (b *firstByteScriptedBody) Close() error { b.closed = true; return nil }

func TestFirstByteEmptyEOFAndPositiveReadWithEOF(t *testing.T) {
	for _, withData := range []bool{false, true} {
		body := &firstByteScriptedBody{withData: withData}
		req := httptest.NewRequest("POST", "http://example.com", nil)
		resp := &http.Response{Body: body}
		ObserveFirstByte(resp, req, time.Now())
		_, err := resp.Body.Read(make([]byte, 1))
		require.NoError(t, err)
		ms, tracked := FirstByteMilliseconds(resp)
		require.True(t, tracked)
		require.Nil(t, ms)
		_, err = resp.Body.Read(make([]byte, 1))
		require.ErrorIs(t, err, io.EOF)
		ms, _ = FirstByteMilliseconds(resp)
		if withData {
			require.NotNil(t, ms)
		} else {
			require.Nil(t, ms)
		}
		require.NoError(t, resp.Body.Close())
		require.True(t, body.closed)
	}
}

func TestFirstByteAttemptsAndConcurrentSnapshotAreIsolated(t *testing.T) {
	req := httptest.NewRequest("POST", "http://example.com", nil)
	first := &http.Response{Body: io.NopCloser(strings.NewReader("old"))}
	second := &http.Response{Body: io.NopCloser(strings.NewReader("new"))}
	ObserveFirstByte(first, req, time.Now().Add(-time.Second))
	ObserveFirstByte(second, req, time.Now())
	_, err := io.ReadAll(first.Body)
	require.NoError(t, err)
	ms, tracked := FirstByteMilliseconds(second)
	require.True(t, tracked)
	require.Nil(t, ms)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 1000 {
			FirstByteMilliseconds(second)
		}
	}()
	_, err = io.ReadAll(second.Body)
	require.NoError(t, err)
	wg.Wait()
	ms, _ = FirstByteMilliseconds(second)
	require.NotNil(t, ms)
	old, _ := FirstByteMilliseconds(first)
	require.GreaterOrEqual(t, *old, 1000)
	require.Less(t, *ms, *old)
}

func TestFirstByteDisabledAndTransportErrors(t *testing.T) {
	wantErr := errors.New("transport failed")
	client := TrackFirstByteClient(&http.Client{Transport: firstByteRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/error" {
			return nil, wantErr
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})})
	req, _ := http.NewRequest("POST", "http://example.com", nil)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	_, tracked := FirstByteMilliseconds(resp)
	require.False(t, tracked)
	req, _ = http.NewRequest("POST", "http://example.com/error", nil)
	resp, err = client.Do(EnableFirstByte(req))
	require.ErrorIs(t, err, wantErr)
	require.Nil(t, resp)
}
