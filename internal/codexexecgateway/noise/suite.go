package noise

const (
	SuiteName = "Noise_hybridIK_X25519+MLKEM768_AESGCM_SHA256"

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
