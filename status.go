package main

import (
	"context"
	"log/slog"
)

// isFinished reports whether a torrent has completed downloading. A nil value
// is treated as false (not finished), since Transmission only omits/leaves it
// unset transiently; treating it as finished could delete an active torrent.
func isFinished(torrent Torrent) bool {
	return torrent.IsFinished != nil && *torrent.IsFinished
}

func Cleanup(ctx context.Context, client *TransmissionClient) (int, error) {
	result := 0
	for {
		torrents, err := client.TorrentGet(ctx, []string{"id", "isFinished"}, nil)
		if err != nil {
			slog.Error("Error getting torrents", "error", err)
			return 0, err
		}
		idsToDelete := make([]int64, 0, len(torrents))
		for _, torrent := range torrents {
			if isFinished(torrent) {
				idsToDelete = append(idsToDelete, torrent.ID)
			}
		}
		if len(idsToDelete) == 0 {
			break
		}
		err = client.TorrentRemove(ctx, idsToDelete, false)
		result += len(idsToDelete)
		if err != nil {
			slog.Error("Error removing torrents", "error", err)
			return 0, err
		}
	}
	return result, nil
}

func StatusOfActiveTorrents(ctx context.Context, client *TransmissionClient) (int, int, error) {
	torrents, err := client.TorrentGet(ctx, []string{"id", "isFinished"}, nil)
	if err != nil {
		slog.Error("Error getting torrents", "error", err)
		return 0, 0, err
	}
	downloaded := 0
	for _, torrent := range torrents {
		if isFinished(torrent) {
			downloaded++
		}
	}
	return len(torrents) - downloaded, downloaded, nil
}
