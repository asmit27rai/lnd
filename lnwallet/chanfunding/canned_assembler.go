package chanfunding

import (
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr/musig2"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	"github.com/lightningnetwork/lnd/fn/v2"
	"github.com/lightningnetwork/lnd/input"
	"github.com/lightningnetwork/lnd/keychain"
)

// NewShimIntent creates a new ShimIntent. This is only used for testing.
func NewShimIntent(localAmt, remoteAmt btcutil.Amount,
	localKey *keychain.KeyDescriptor, remoteKey *btcec.PublicKey,
	chanPoint *wire.OutPoint, thawHeight uint32, musig2 bool) *ShimIntent {

	return &ShimIntent{
		localFundingAmt:  localAmt,
		remoteFundingAmt: remoteAmt,
		localKey:         localKey,
		remoteKey:        remoteKey,
		chanPoint:        chanPoint,
		thawHeight:       thawHeight,
		musig2:           musig2,
	}
}

// ShimIntent is an intent created by the CannedAssembler which represents a
// funding output to be created that was constructed outside the wallet. This
// might be used when a hardware wallet, or a channel factory is the entity
// crafting the funding transaction, and not lnd.
type ShimIntent struct {
	// localFundingAmt is the final amount we put into the funding output.
	localFundingAmt btcutil.Amount

	// remoteFundingAmt is the final amount the remote party put into the
	// funding output.
	remoteFundingAmt btcutil.Amount

	// localKey is our multi-sig key.
	localKey *keychain.KeyDescriptor

	// remoteKey is the remote party's multi-sig key.
	remoteKey *btcec.PublicKey

	// chanPoint is the final channel point for the to be created channel.
	chanPoint *wire.OutPoint

	// thawHeight, if non-zero is the height where this channel will become
	// a normal channel. Until this height, it's considered frozen, so it
	// can only be cooperatively closed by the responding party.
	thawHeight uint32

	// musig2 determines if the funding output should use musig2 to
	// generate an aggregate key to use as the taproot-native multi-sig
	// output.
	musig2 bool

	// tapscriptRoot is the root of the tapscript tree that will be used to
	// create the funding output. This field will only be utilized if the
	// MuSig2 flag above is set to true.
	//
	// TODO(roasbeef): fold above into new chan type? sum type like thing,
	// includes the tapscript root, etc
	tapscriptRoot fn.Option[chainhash.Hash]
}

// FundingOutput returns the witness script, and the output that creates the
// funding output.
//
// NOTE: This method satisfies the chanfunding.Intent interface.
func (s *ShimIntent) FundingOutput() ([]byte, *wire.TxOut, error) {
	if s.localKey == nil || s.remoteKey == nil {
		return nil, nil, fmt.Errorf("unable to create witness " +
			"script, no funding keys")
	}

	totalAmt := s.localFundingAmt + s.remoteFundingAmt

	// If musig2 is active, then we'll return a single aggregated key
	// rather than using the "existing" funding script.
	if s.musig2 {
		// Similar to the existing p2wsh script, we'll always ensure
		// the keys are sorted before use.
		return input.GenTaprootFundingScript(
			s.localKey.PubKey, s.remoteKey, int64(totalAmt),
			s.tapscriptRoot,
		)
	}

	return input.GenFundingPkScript(
		s.localKey.PubKey.SerializeCompressed(),
		s.remoteKey.SerializeCompressed(),
		int64(totalAmt),
	)
}

// TaprootInternalKey may return the internal key for a MuSig2 funding output,
// but only if this is actually a MuSig2 channel.
func (s *ShimIntent) TaprootInternalKey() fn.Option[*btcec.PublicKey] {
	if !s.musig2 {
		return fn.None[*btcec.PublicKey]()
	}

	// Similar to the existing p2wsh script, we'll always ensure the keys
	// are sorted before use. Since we're only interested in the internal
	// key, we don't need to take into account any tapscript root.
	//
	// We ignore the error here as this is only called after FundingOutput
	// is called.
	combinedKey, _, _, _ := musig2.AggregateKeys(
		[]*btcec.PublicKey{s.localKey.PubKey, s.remoteKey}, true,
	)

	return fn.Some(combinedKey.PreTweakedKey)
}

// Cancel allows the caller to cancel a funding Intent at any time.  This will
// return any resources such as coins back to the eligible pool to be used in
// order channel fundings.
//
// NOTE: This method satisfies the chanfunding.Intent interface.
func (s *ShimIntent) Cancel() {
}

// LocalFundingAmt is the amount we put into the channel. This may differ from
// the local amount requested, as depending on coin selection, we may bleed
// from of that LocalAmt into fees to minimize change.
//
// NOTE: This method satisfies the chanfunding.Intent interface.
func (s *ShimIntent) LocalFundingAmt() btcutil.Amount {
	return s.localFundingAmt
}

// RemoteFundingAmt is the amount the remote party put into the channel.
//
// NOTE: This method satisfies the chanfunding.Intent interface.
func (s *ShimIntent) RemoteFundingAmt() btcutil.Amount {
	return s.remoteFundingAmt
}

// ChanPoint returns the final outpoint that will create the funding output
// described above.
//
// NOTE: This method satisfies the chanfunding.Intent interface.
func (s *ShimIntent) ChanPoint() (*wire.OutPoint, error) {
	if s.chanPoint == nil {
		return nil, fmt.Errorf("chan point unknown, funding output " +
			"not constructed")
	}

	return s.chanPoint, nil
}

// ThawHeight returns the height where this channel goes back to being a normal
// channel.
func (s *ShimIntent) ThawHeight() uint32 {
	return s.thawHeight
}

// Inputs returns all inputs to the final funding transaction that we
// know about. For the ShimIntent this will always be none, since it is funded
// externally.
func (s *ShimIntent) Inputs() []wire.OutPoint {
	return nil
}

// Outputs returns all outputs of the final funding transaction that we
// know about. Since this is an externally funded channel, the channel output
// is the only known one.
func (s *ShimIntent) Outputs() []*wire.TxOut {
	_, txOut, err := s.FundingOutput()
	if err != nil {
		log.Warnf("Unable to find funding output for shim intent: %v",
			err)

		// Failed finding funding output, return empty list of known
		// outputs.
		return nil
	}

	return []*wire.TxOut{txOut}
}

// FundingKeys couples our multi-sig key along with the remote party's key.
type FundingKeys struct {
	// LocalKey is our multi-sig key.
	LocalKey *keychain.KeyDescriptor

	// RemoteKey is the multi-sig key of the remote party.
	RemoteKey *btcec.PublicKey
}

// MultiSigKeys returns the committed multi-sig keys, but only if they've been
// specified/provided.
func (s *ShimIntent) MultiSigKeys() (*FundingKeys, error) {
	if s.localKey == nil || s.remoteKey == nil {
		return nil, fmt.Errorf("unknown funding keys")
	}

	return &FundingKeys{
		LocalKey:  s.localKey,
		RemoteKey: s.remoteKey,
	}, nil
}

// A compile-time check to ensure ShimIntent adheres to the Intent interface.
var _ Intent = (*ShimIntent)(nil)

// CannedAssembler is a type of chanfunding.Assembler wherein the funding
// transaction is constructed outside of lnd, and may already exist. This
// Assembler serves as a shim which gives the funding flow the only thing it
// actually needs to proceed: the channel point.
type CannedAssembler struct {
	// fundingAmt is the total amount of coins in the funding output.
	fundingAmt btcutil.Amount

	// localKey is our multi-sig key.
	localKey *keychain.KeyDescriptor

	// remoteKey is the remote party's multi-sig key.
	remoteKey *btcec.PublicKey

	// chanPoint is the final channel point for the to be created channel.
	chanPoint wire.OutPoint

	// initiator indicates if we're the initiator or the channel or not.
	initiator bool

	// thawHeight, if non-zero is the height where this channel will become
	// a normal channel. Until this height, it's considered frozen, so it
	// can only be cooperatively closed by the responding party.
	thawHeight uint32

	// musig2 determines if the funding output should use musig2 to
	// generate an aggregate key to use as the taproot-native multi-sig
	// output.
	musig2 bool
}

// NewCannedAssembler creates a new CannedAssembler from the material required
// to construct a funding output and channel point.
//
// TODO(roasbeef): pass in chan type instead?
func NewCannedAssembler(thawHeight uint32, chanPoint wire.OutPoint,
	fundingAmt btcutil.Amount, localKey *keychain.KeyDescriptor,
	remoteKey *btcec.PublicKey, initiator, musig2 bool) *CannedAssembler {

	return &CannedAssembler{
		initiator:  initiator,
		localKey:   localKey,
		remoteKey:  remoteKey,
		fundingAmt: fundingAmt,
		chanPoint:  chanPoint,
		thawHeight: thawHeight,
		musig2:     musig2,
	}
}

// ProvisionChannel creates a new ShimIntent given the passed funding Request.
// The returned intent is immediately able to provide the channel point and
// funding output as they've already been created outside lnd.
//
// NOTE: This method satisfies the chanfunding.Assembler interface.
func (c *CannedAssembler) ProvisionChannel(req *Request) (Intent, error) {
	// We'll exit out if SubtractFees is set as the funding transaction has
	// already been assembled, so we don't influence coin selection.
	if req.SubtractFees {
		return nil, fmt.Errorf("SubtractFees ignored, funding " +
			"transaction is frozen")
	}

	// We'll exit out if FundUpToMaxAmt or MinFundAmt is set as the funding
	// transaction has already been assembled, so we don't influence coin
	// selection.
	if req.FundUpToMaxAmt != 0 || req.MinFundAmt != 0 {
		return nil, fmt.Errorf("FundUpToMaxAmt and MinFundAmt " +
			"ignored, funding transaction is frozen")
	}

	intent := &ShimIntent{
		localKey:   c.localKey,
		remoteKey:  c.remoteKey,
		chanPoint:  &c.chanPoint,
		thawHeight: c.thawHeight,
		musig2:     c.musig2,
	}

	if c.initiator {
		intent.localFundingAmt = c.fundingAmt
	} else {
		intent.remoteFundingAmt = c.fundingAmt
	}

	// A simple sanity check to ensure the provisioned request matches the
	// re-made shim intent.
	if req.LocalAmt+req.RemoteAmt != c.fundingAmt {
		return nil, fmt.Errorf("intent doesn't match canned "+
			"assembler: local_amt=%v, remote_amt=%v, funding_amt=%v",
			req.LocalAmt, req.RemoteAmt, c.fundingAmt)
	}

	return intent, nil
}

// A compile-time assertion to ensure CannedAssembler meets the Assembler
// interface.
var _ Assembler = (*CannedAssembler)(nil)
