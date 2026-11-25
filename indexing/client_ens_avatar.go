package indexing

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/ysh0566/ponder-go/abi"
)

// ENSAvatarOptions selects the ENS record block and optional media gateways.
// HTTPClient can supply a deployment-specific egress transport for remote assets.
type ENSAvatarOptions struct {
	ENSNameOptions
	IPFSGateway    string
	ArweaveGateway string
	HTTPClient     *http.Client
}

var avatarNFTABI = sync.OnceValues(func() (abi.ABI, error) {
	return abi.Parse(`[
	{"name":"tokenURI","type":"function","stateMutability":"view","inputs":[{"type":"uint256"}],"outputs":[{"type":"string"}]},
	{"name":"uri","type":"function","stateMutability":"view","inputs":[{"type":"uint256"}],"outputs":[{"type":"string"}]}
	]`)
})

// GetEnsAvatar reads the "avatar" text record at the event or selected block.
// It resolves image and NFT references, returning nil for missing or unusable
// media. Resolver errors follow the same strictness rules as GetEnsText.
func (c *Client) GetEnsAvatar(ctx context.Context, name string, options ...ENSAvatarOptions) (*string, error) {
	if len(options) > 1 {
		return nil, fmt.Errorf("GetEnsAvatar accepts at most one options object")
	}
	var opt ENSAvatarOptions
	if len(options) == 1 {
		opt = options[0]
	}
	ctx, finish, err := c.set.managedRoots.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	record, err := c.GetEnsText(ctx, name, "avatar", opt.ENSNameOptions)
	if err != nil || record == nil {
		return nil, err
	}
	uri := *record
	if strings.Contains(strings.ToLower(uri), "eip155:") {
		var nft avatarNFT
		nft, err = parseAvatarNFT(uri)
		if err == nil {
			uri, err = c.avatarNFTImage(ctx, nft, opt)
		}
	}
	if err == nil {
		uri, err = opt.imageURI(ctx, uri)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, nil
	}
	return &uri, nil
}

type avatarNFT struct {
	address, method, tokenText string
	id                         *big.Int
}

func parseAvatarNFT(reference string) (avatarNFT, error) {
	if did, ok := strings.CutPrefix(reference, "did:nft:"); ok {
		reference = strings.ReplaceAll(did, "_", "/")
	}
	parts := strings.Split(reference, "/")
	if len(parts) < 3 || parts[2] == "" {
		return avatarNFT{}, fmt.Errorf("invalid NFT avatar reference")
	}
	chain, id, _ := strings.Cut(parts[0], ":")
	kind, address, _ := strings.Cut(parts[1], ":")
	if !strings.EqualFold(chain, "eip155") || id == "" {
		return avatarNFT{}, fmt.Errorf("invalid NFT chain")
	}
	if _, ok := contractAddress(address); !ok {
		return avatarNFT{}, fmt.Errorf("invalid NFT contract")
	}
	method := "tokenURI"
	switch strings.ToLower(kind) {
	case "erc721":
	case "erc1155":
		method = "uri"
	default:
		return avatarNFT{}, fmt.Errorf("unsupported NFT namespace")
	}
	token := parts[2]
	base := 10
	digits := token
	if strings.HasPrefix(strings.ToLower(token), "0x") {
		base, digits = 16, token[2:]
	}
	n, ok := new(big.Int).SetString(digits, base)
	if !ok || n.Sign() < 0 || n.BitLen() > 256 {
		return avatarNFT{}, fmt.Errorf("invalid NFT token ID")
	}
	return avatarNFT{address: address, method: method, tokenText: token, id: n}, nil
}

func (c *Client) avatarNFTImage(ctx context.Context, nft avatarNFT, opt ENSAvatarOptions) (string, error) {
	parsed, err := avatarNFTABI()
	if err != nil {
		return "", err
	}
	data, fn, err := encodeContractCall(ContractCall{ABI: parsed, FunctionName: nft.method, Args: []any{nft.id}})
	if err != nil {
		return "", err
	}
	// viem reads the ENS record at the requested block and the NFT metadata
	// with an unqualified contract read (latest) on the same chain client.
	output, err := c.Call(ctx, Object{"to": nft.address, "data": data}, CallOptions{BlockOptions: BlockOptions{Cache: "immutable"}, CCIPRead: opt.CCIPRead})
	if err != nil {
		return "", err
	}
	if output == nil {
		return "", fmt.Errorf("empty NFT URI response")
	}
	result, err := decodeContractResult(fn, *output)
	if err != nil {
		return "", err
	}
	uri, ok := result.(string)
	if !ok {
		return "", fmt.Errorf("invalid NFT URI")
	}
	return opt.metadataImage(ctx, uri, nft)
}

func (opt ENSAvatarOptions) metadataImage(ctx context.Context, uri string, nft avatarNFT) (string, error) {
	uri, inline, err := opt.resolveURI(uri)
	if err != nil {
		return "", err
	}
	if inline && strings.HasPrefix(uri, "{") {
		return avatarJSONImage([]byte(uri))
	}
	if inline && strings.HasPrefix(uri, "data:") {
		header, payload, ok := strings.Cut(uri, ",")
		if !ok {
			return "", fmt.Errorf("invalid metadata data URI")
		}
		decoded := payload
		if header != "data:application/json;base64" {
			decoded, err = url.PathUnescape(payload)
			if err != nil {
				return "", err
			}
		}
		if !strings.HasSuffix(header, ";base64") {
			return avatarJSONImage([]byte(decoded))
		}
		decoded = strings.Map(func(r rune) rune {
			if strings.ContainsRune(" \t\r\n\f", r) {
				return -1
			}
			return r
		}, decoded)
		raw, err := base64.StdEncoding.DecodeString(decoded)
		if err != nil {
			raw, err = base64.RawStdEncoding.DecodeString(decoded)
		}
		if err != nil {
			return "", err
		}
		// The JSON data URI branch of viem uses atob, yielding one JS
		// code point per byte. Fetching other MIME types decodes UTF-8.
		if header == "data:application/json;base64" {
			text := make([]rune, len(raw))
			for i, b := range raw {
				text[i] = rune(b)
			}
			return avatarJSONImage([]byte(string(text)))
		}
		return avatarJSONImage(raw)
	}
	token := nft.tokenText
	// ENSIP NFT templates retain the source ID's spelling. ERC-1155 also
	// strips its 0x prefix and pads the ID to 64 characters.
	if nft.method == "uri" {
		token = strings.Replace(token, "0x", "", 1)
		if len(token) < 64 {
			token = strings.Repeat("0", 64-len(token)) + token
		}
	}
	if at := strings.Index(uri, "{id}"); at >= 0 {
		left := strings.TrimSuffix(uri[:at], "0x")
		uri = left + token + uri[at+4:]
	}
	resp, err := opt.fetch(ctx, http.MethodGet, uri)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	const maxMetadata = 4 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxMetadata+1))
	if err != nil {
		return "", err
	}
	if len(raw) > maxMetadata {
		return "", fmt.Errorf("NFT metadata exceeds %d bytes", maxMetadata)
	}
	return avatarJSONImage(raw)
}

func avatarJSONImage(raw []byte) (string, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return "", err
	}
	for _, key := range []string{"image", "image_url", "image_data"} {
		v := fields[key]
		if len(v) == 0 || string(v) == "null" || string(v) == "false" || string(v) == "0" {
			continue
		}
		var image string
		if err := json.Unmarshal(v, &image); err != nil {
			return "", err
		}
		if image != "" {
			return image, nil
		}
	}
	return "", fmt.Errorf("NFT metadata has no image")
}

// URI recognition follows viem 2.35.0 (MIT, see licenses/npm/viem@2.35.0).
var (
	avatarNetwork    = regexp.MustCompile(`(https?://[^/]*|ipfs:/|ipns:/|ar:/)?/?(ipfs/|ipns/)?([\w.\-]+)(/.*)?`)
	avatarCID        = regexp.MustCompile(`^(Qm[1-9A-HJ-NP-Za-km-z]{44,}|b[A-Za-z2-7]{58,}|B[A-Z2-7]{58,}|z[1-9A-HJ-NP-Za-km-z]{48,}|F[0-9A-F]{50,})(/[\w.\-]+)?(/.*)?$`)
	avatarBase64     = regexp.MustCompile(`^data:[a-zA-Z/+\-]*;base64,[^"]`)
	avatarDataPrefix = regexp.MustCompile(`^data:[a-zA-Z/+\-]*(;[a-zA-Z0-9].*?)?,`)
)

func (opt ENSAvatarOptions) resolveURI(uri string) (string, bool, error) {
	if avatarBase64.MatchString(uri) {
		return uri, true, nil
	}
	m := avatarNetwork.FindStringSubmatch(uri)
	if len(m) > 0 {
		path := ""
		switch {
		case m[1] == "ipns:/" || m[2] == "ipns/":
			path = "/ipns/"
		case m[1] == "ipfs:/" || m[2] == "ipfs/" || avatarCID.MatchString(uri):
			path = "/ipfs/"
		}
		if path != "" {
			gateway := opt.IPFSGateway
			if gateway == "" {
				gateway = "https://ipfs.io"
			}
			return strings.TrimSuffix(gateway, "/") + path + m[3] + m[4], false, nil
		}
		if strings.HasPrefix(uri, "http") {
			if opt.ArweaveGateway != "" {
				uri = strings.ReplaceAll(uri, "https://arweave.net", opt.ArweaveGateway)
			}
			return uri, false, nil
		}
		if m[1] == "ar:/" {
			gateway := opt.ArweaveGateway
			if gateway == "" {
				gateway = "https://arweave.net"
			}
			return strings.TrimSuffix(gateway, "/") + "/" + m[3] + m[4], false, nil
		}
	}
	uri = avatarDataPrefix.ReplaceAllString(uri, "")
	if strings.HasPrefix(uri, "<svg") {
		// btoa encodes Latin-1 code points, as on the pinned Node runtime.
		data := make([]byte, 0, len(uri))
		for _, r := range uri {
			if r > 255 {
				return "", false, fmt.Errorf("SVG is not Latin-1")
			}
			data = append(data, byte(r))
		}
		uri = "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(data)
	}
	if strings.HasPrefix(uri, "data:") || strings.HasPrefix(uri, "{") {
		return uri, true, nil
	}
	return "", false, fmt.Errorf("invalid ENS avatar URI")
}

func (opt ENSAvatarOptions) fetch(ctx context.Context, method, uri string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, uri, nil)
	if err != nil {
		return nil, err
	}
	if (req.URL.Scheme != "http" && req.URL.Scheme != "https") || req.URL.Host == "" || req.URL.User != nil {
		return nil, fmt.Errorf("invalid avatar HTTP URL")
	}
	client := opt.HTTPClient
	if client == nil {
		client = publicGatewayClient
	}
	return client.Do(req)
}

func (opt ENSAvatarOptions) imageURI(ctx context.Context, uri string) (string, error) {
	resolved, inline, err := opt.resolveURI(uri)
	if err != nil || inline {
		return resolved, err
	}
	res, err := opt.fetch(ctx, http.MethodHead, resolved)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "image/") {
		return "", fmt.Errorf("avatar URL did not return an image")
	}
	return resolved, nil
}
