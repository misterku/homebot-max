package main

import (
	"context"
	"log/slog"

	"github.com/hekmon/transmissionrpc/v3"
)

func Cleanup(ctx context.Context, client *transmissionrpc.Client) (int, error) {
	result := 0
	for {
		torrents, err := client.TorrentGet(ctx, []string{"id", "isFinished", "status"}, nil)
		if err != nil {
			slog.Error("Error getting torrents", "error", err)
			return 0, err
		}
		idsToDelete := make([]int64, 0, len(torrents))
		for _, torrent := range torrents {
			if *torrent.Status == 6 {
				idsToDelete = append(idsToDelete, *torrent.ID)
			}
		}
		if len(idsToDelete) == 0 {
			break
		}
		err = client.TorrentRemove(
			ctx,
			transmissionrpc.TorrentRemovePayload{
				IDs: idsToDelete,
				DeleteLocalData: false,
			},
		)
		result += len(idsToDelete)
		if err != nil {
			slog.Error("Error removing torrents", "error", err)
			return 0, err
		}
	}
	return result, nil
}

func StatusOfActiveTorrents(ctx context.Context, client *transmissionrpc.Client) (int, int, error) {
	torrents, err := client.TorrentGet(ctx, []string{"id", "isFinished", "status"}, nil)
	if err != nil {
		slog.Error("Error getting torrents", "error", err)
		return 0, 0, err
	}
	idsToDelete := make([]int64, 0, len(torrents))
	for _, torrent := range torrents {
		if *torrent.Status == 6 {
			idsToDelete = append(idsToDelete, *torrent.ID)
		}
	}
	return len(torrents) - len(idsToDelete), len(idsToDelete), nil
}