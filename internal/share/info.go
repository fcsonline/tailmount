package share

// Tunnel ports served by a tailmount sharer. Clients dial these through the
// tailcat tunnel; nothing listens on them on the host network.
const (
	// PortNFS carries the NFSv3 and MOUNT protocols on one TCP stream.
	PortNFS uint16 = 2049
	// PortInfo returns one JSON Info document per connection, then closes.
	PortInfo uint16 = 2050
)

// Access modes a share can be published with.
const (
	ModeReadWrite = "rw"
	ModeReadOnly  = "ro"
)

// Info describes a share to a client before it mounts. The client needs the
// mode to pick the right mount flag and the name to choose a mountpoint.
type Info struct {
	Mode    string `json:"mode"`
	Name    string `json:"name"`
	Version string `json:"version"`
}
