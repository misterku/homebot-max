package main

import (
	"net/url"
	"os"
	"context"
	"fmt"


	transmissionrpc "github.com/hekmon/transmissionrpc/v3"
)

func NewTransmissionClient(username string, password string, host string, scheme string, port int) *transmissionrpc.Client {
	endpoint, err := url.Parse(fmt.Sprintf("%s://%s:%s@%s:%d/transmission/rpc", scheme, username, password, host, port))
	if err != nil {
		panic(err)
	}
	tbt, err := transmissionrpc.New(endpoint, nil)
	if err != nil {
		panic(err)
	}
	ok, serverVersion, serverMinimumVersion, err := tbt.RPCVersion(context.TODO())
	if err != nil {
		panic(err)
	}
	if !ok {
		panic(fmt.Sprintf("Remote transmission RPC version (v%d) is incompatible with the transmission library (v%d): remote needs at least v%d",
			serverVersion, transmissionrpc.RPCVersion, serverMinimumVersion))
	}
	return tbt
}

func SendFileToTransmission(client *transmissionrpc.Client, fileID string, chatID int64) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	filename:= dir + "/downloads/" + fileID + ".torrent"
	_, err = client.TorrentAddFile(context.TODO(), filename)
	if err != nil {
		return err
	}
	return nil
}
