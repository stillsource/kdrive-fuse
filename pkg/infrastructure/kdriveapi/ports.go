package kdriveapi

import "github.com/stillsource/kdrive-fuse/pkg/service"

// The service operations live on the sub-services (reached via client.Files /
// client.Shares), so the ports are satisfied there, not on *Client.
var (
	_ service.FileReader  = (*FilesService)(nil)
	_ service.FileWriter  = (*FilesService)(nil)
	_ service.FileManager = (*FilesService)(nil)
	_ service.Sharer      = (*SharesService)(nil)
)
