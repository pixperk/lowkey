package client

import "context"

type Lock struct {
	client       *Client
	name         string
	fencingToken uint64
}

func (l *Lock) Token() uint64 {
	return l.fencingToken
}

func (l *Lock) Release(ctx context.Context) error {
	return l.client.Release(ctx, l.name)
}
