package noise

const (
	// SuiteName is codex's public registry tag for the channel — what the
	// executor advertises in registration metadata. Note the "X25519"
	// spelling here is OPPOSITE of what goes into the actual Noise
	// protocol-name string below.
	SuiteName = "Noise_hybridIK_X25519+MLKEM768_AESGCM_SHA256"

	// NoiseProtocolName is the string clatter feeds into SymmetricState
	// initialization. It is built from each crypto component's name()
	// trait method — clatter's X25519::name() returns "25519" (per RFC
	// 8709 / standard Noise naming), NOT "X25519" like the public suite
	// tag uses. Getting this wrong silently corrupts the initial h and
	// every later AEAD nonce-prefixed hash.
	NoiseProtocolName = "Noise_hybridIK_25519+MLKEM768_AESGCM_SHA256"

	PatternName    = "hybridIK"
	PrologueDomain = "codex-exec-server-relay-noise/v1"

	MaxMessageLen = 65535

	X25519PubLen   = 32
	X25519PrivLen  = 32
	X25519ShareLen = 32

	MLKEM768PubLen       = 1184
	MLKEM768SecretLen    = 2400
	MLKEM768CiphertextLen = 1088
	MLKEM768SharedLen    = 32

	AESGCMKeyLen   = 32
	AESGCMNonceLen = 12
	AESGCMTagLen   = 16

	SHA256HashLen = 32
)
