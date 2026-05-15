package join

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetCommand_DefaultStakeMatchesValidatorMinSelfStakeSemantics(t *testing.T) {
	cmd := GetCommand()

	flag := cmd.Flags().Lookup(flagStakeXGR)
	require.NotNil(t, flag)
	require.Equal(t, "200000", flag.DefValue)
	require.Contains(t, flag.Usage, "default 200,000")
}

func TestGetCommand_StakeValidationRejectsNonInteger(t *testing.T) {
	cmd := GetCommand()
	require.NoError(t, cmd.Flags().Set("jsonrpc", "http://127.0.0.1:8545"))
	require.NoError(t, cmd.Flags().Set(flagDataDir, "/tmp/data"))
	require.NoError(t, cmd.Flags().Set(flagStakeXGR, "200000.5"))

	err := cmd.PreRunE(cmd, nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "invalid --stake")
}

func TestJoinResult_OutputExplicitlyMentionsTwoStageEligibility(t *testing.T) {
	res := (&JoinResult{
		Validator:       "0x1",
		JoinedAtBlock:   123,
		BLSRegistered:   true,
		StakeWei:        "200000000000000000000000",
		AccountStakeWei: "200000000000000000000000",
		Eligible:        true, // current CLI behavior: true after successful join flow
		Note:            "Join requires >=200k self stake and becomes effective in next epoch. Eligibility in fetcher still additionally requires >=2M effective total support stake.",
	}).GetOutput()

	require.Contains(t, res, "Eligible             = true")
	require.Contains(t, res, ">=200k self stake")
	require.Contains(t, res, ">=2M effective total support stake")
}
