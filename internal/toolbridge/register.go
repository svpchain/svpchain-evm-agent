package toolbridge

const (
	SkillAuth = "svpchain-auth"
	SkillEVM  = "svpchain-evm"
	SkillMeta = "svpchain-meta"
)

func NewEmpty() *Registry { return newRegistry() }
