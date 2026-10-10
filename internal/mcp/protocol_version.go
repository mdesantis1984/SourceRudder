package mcp

// 2025-03-26 is excluded because it requires receiving JSON-RPC batches.
var supportedProtocolVersions = [...]string{"2025-11-25", "2025-06-18", "2024-11-05"}

func isSupportedProtocolVersion(version string) bool {
	for _, supported := range supportedProtocolVersions {
		if version == supported {
			return true
		}
	}
	return false
}

func negotiateProtocolVersion(requested string) string {
	if isSupportedProtocolVersion(requested) {
		return requested
	}
	return supportedProtocolVersions[0]
}
