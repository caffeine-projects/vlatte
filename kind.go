package vlatte

// Kind names the protocol of a mock server, such as "http".
// Each protocol package declares its own, for example mockhttp.Kind, so adding a protocol doesn't change this package.
// Protocol-neutral components, such as event listeners ([Listener], [OnComplete]), can use it to tell their inputs
// apart.
type Kind string

// String implements [fmt.Stringer].
func (k Kind) String() string {
	return string(k)
}
