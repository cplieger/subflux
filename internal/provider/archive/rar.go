package archive

import (
	"bytes"
	"errors"
	"io"

	"github.com/nwaples/rardecode/v2"
)

// maxRARDictionary bounds the window a rar member may declare: rardecode
// allocates it inside Next, and its default allows 4 GiB.
const maxRARDictionary = 64 << 20

// rarMembers walks data's rar headers, at most maxEntries of them. A rar is a
// stream, so each member's read opens the archive again and walks to it.
func rarMembers(data []byte) ([]member, error) {
	r, err := openRAR(data)
	if err != nil {
		return nil, err
	}
	var ms []member
	for {
		hdr, err := r.Next()
		if errors.Is(err, io.EOF) {
			return ms, nil
		}
		if err != nil {
			return nil, refuse(err, "rar header %d", len(ms)+1)
		}
		if len(ms) == maxEntries {
			return nil, refuse(nil, "rar holds more than %d headers", maxEntries)
		}
		k := len(ms)
		ms = append(ms, member{
			name:        hdr.Name,
			packed:      hdr.PackedSize,
			unpacked:    hdr.UnPackedSize,
			sizeUnknown: hdr.UnKnownSize,
			isDir:       hdr.IsDir,
			read:        func() ([]byte, error) { return readRARMember(data, k) },
		})
	}
}

func openRAR(data []byte) (*rardecode.Reader, error) {
	r, err := rardecode.NewReader(bytes.NewReader(data), rardecode.MaxDictionarySize(maxRARDictionary))
	switch {
	case errors.Is(err, rardecode.ErrNoSig):
		return nil, errNoContainer
	case err != nil:
		return nil, refuse(err, "rar")
	}
	return r, nil
}

// readRARMember reads the k-th header's member.
func readRARMember(data []byte, k int) ([]byte, error) {
	r, err := openRAR(data)
	if err != nil {
		return nil, err
	}
	var hdr *rardecode.FileHeader
	for i := range k + 1 {
		if hdr, err = r.Next(); err != nil {
			return nil, refuse(err, "rar header %d", i+1)
		}
	}
	return readMember(hdr.Name, r)
}
