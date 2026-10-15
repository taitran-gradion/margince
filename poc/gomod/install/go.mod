module example.com/acme-margince

go 1.27.2

require (
	example.com/margince-ext/my-extension v0.2.1
	github.com/margince/margince/backend v0.0.0
	github.com/margince/margince/composition v0.0.0
	github.com/margince/margince/extensions/de v0.0.0
	github.com/margince/margince/extensions/openchannel v0.0.0
	github.com/margince/margince/extensions/vn v0.0.0
)

// Stand-in for a backend/vX tag.
replace github.com/margince/margince/backend => ../../../backend

// The installation decides which composition core links; core's own
// replace (backend/go.mod -> ../composition) is ignored outside the main module.
replace github.com/margince/margince/composition => ../../../build/composition/backend

replace (
	github.com/margince/margince/extensions/de => ../../../extensions/de
	github.com/margince/margince/extensions/openchannel => ../../../extensions/openchannel
	github.com/margince/margince/extensions/vn => ../../../extensions/vn
)
