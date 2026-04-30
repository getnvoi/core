package compile

// Bundle is the set of generated `.tf` files to materialize in the work
// directory before invoking terraform. Filename is the key; one bundle =
// one TF root module.
//
// Bytes-based: emitters render their HCL however they like (templates,
// hclwrite, hand-written) and hand back rendered bytes. The bundle is
// just a file collator — no opinions about how each file was generated.
type Bundle struct {
	files map[string][]byte
}

func NewBundle() *Bundle { return &Bundle{files: map[string][]byte{}} }

// Set attaches rendered bytes to the bundle under name. Overwrites any
// prior entry.
func (b *Bundle) Set(name string, content []byte) { b.files[name] = content }

// Render returns the file map as-is. The lifecycle in cmd/cli writes
// these to disk under runtime.WorkDir.
func (b *Bundle) Render() (map[string][]byte, error) {
	out := make(map[string][]byte, len(b.files))
	for k, v := range b.files {
		out[k] = v
	}
	return out, nil
}
