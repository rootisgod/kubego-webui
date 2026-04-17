package kubevirt

import "errors"

// ErrNotImplemented marks driver methods that have not yet been wired up
// against KubeVirt. Handlers surface this as HTTP 501 so the UI can
// indicate "coming soon" instead of "broken".
var ErrNotImplemented = errors.New("not implemented on KubeVirt yet")
