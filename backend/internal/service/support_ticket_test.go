package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateTicketSubject(t *testing.T) {
	_, err := validateTicketSubject("   ")
	require.Error(t, err)
	_, err = validateTicketSubject(strings.Repeat("x", 201))
	require.Error(t, err)
	ok, err := validateTicketSubject("  无法调用 claude  ")
	require.NoError(t, err)
	require.Equal(t, "无法调用 claude", ok)
}

func TestValidateTicketBody(t *testing.T) {
	_, err := validateTicketBody("")
	require.Error(t, err)
	_, err = validateTicketBody(strings.Repeat("y", 20001))
	require.Error(t, err)
	ok, err := validateTicketBody("detail")
	require.NoError(t, err)
	require.Equal(t, "detail", ok)
}

func TestTicketServiceNilDB(t *testing.T) {
	svc := NewTicketService(nil)
	_, err := svc.Create(context.Background(), 1, "s", "b")
	require.Error(t, err)
	_, err = svc.ListMine(context.Background(), 1)
	require.Error(t, err)
	_, err = svc.ListAll(context.Background(), "", "", 0, 0)
	require.Error(t, err)
}
