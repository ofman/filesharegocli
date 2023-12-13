package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/ipfs/boxo/files"
	"github.com/ipfs/boxo/path"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/kubo/config"
	"github.com/ipfs/kubo/core"
	"github.com/ipfs/kubo/core/coreapi"
	icore "github.com/ipfs/kubo/core/coreiface"
	"github.com/ipfs/kubo/core/node/libp2p"
	"github.com/ipfs/kubo/plugin/loader"
	"github.com/ipfs/kubo/repo/fsrepo"
	"github.com/schollz/progressbar/v3"
)

var flagExp = flag.Bool("experimental", false, "enable experimental features")

func SetupPlugins(externalPluginsPath string) error {
	// Load any external plugins if available on externalPluginsPath
	plugins, err := loader.NewPluginLoader(filepath.Join(externalPluginsPath, "plugins"))
	if err != nil {
		return fmt.Errorf("error loading plugins: %s", err)
	}

	// Load preloaded and external plugins
	if err := plugins.Initialize(); err != nil {
		return fmt.Errorf("error initializing plugins: %s", err)
	}

	if err := plugins.Inject(); err != nil {
		return fmt.Errorf("error initializing plugins: %s", err)
	}

	return nil
}

func CreateTempRepo() (string, error) {
	repoPath, err := os.MkdirTemp("", "ipfs-shell")
	if err != nil {
		return "", fmt.Errorf("failed to get temp dir: %s", err)
	}

	// Create a config with default options and a 2048 bit key
	cfg, err := config.Init(io.Discard, 2048)
	if err != nil {
		return "", err
	}

	// When creating the repository, you can define custom settings on the repository, such as enabling experimental
	// features (See experimental-features.md) or customizing the gateway endpoint.
	// To do such things, you should modify the variable `cfg`. For example:
	if *flagExp {
		// https://github.com/ipfs/kubo/blob/master/docs/experimental-features.md#ipfs-filestore
		cfg.Experimental.FilestoreEnabled = true
		// https://github.com/ipfs/kubo/blob/master/docs/experimental-features.md#ipfs-urlstore
		cfg.Experimental.UrlstoreEnabled = true
		// https://github.com/ipfs/kubo/blob/master/docs/experimental-features.md#ipfs-p2p
		cfg.Experimental.Libp2pStreamMounting = true
		// https://github.com/ipfs/kubo/blob/master/docs/experimental-features.md#p2p-http-proxy
		cfg.Experimental.P2pHttpProxy = true
		// See also: https://github.com/ipfs/kubo/blob/master/docs/config.md
		// And: https://github.com/ipfs/kubo/blob/master/docs/experimental-features.md
	}

	// Create the repo with the config
	err = fsrepo.Init(repoPath, cfg)
	if err != nil {
		return "", fmt.Errorf("failed to init ephemeral node: %s", err)
	}

	return repoPath, nil
}

// Creates an IPFS node and returns its coreAPI.
func CreateNode(ctx context.Context, repoPath string) (*core.IpfsNode, error) {
	// Open the repo
	repo, err := fsrepo.Open(repoPath)
	if err != nil {
		return nil, err
	}

	// Construct the node

	nodeOptions := &core.BuildCfg{
		Online:  true,
		Routing: libp2p.DHTOption, // This option sets the node to be a full DHT node (both fetching and storing DHT Records)
		// Routing: libp2p.DHTClientOption, // This option sets the node to be a client DHT node (only fetching records)
		Repo: repo,
	}

	return core.NewNode(ctx, nodeOptions)
}

func GetUnixfsNode(path string) (files.Node, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	f, err := files.NewSerialFile(path, false, st)
	if err != nil {
		return nil, err
	}

	return f, nil
}

func ForeverSpin() {
	bar := progressbar.Default(-1)
	for {
		bar.Add(1)
		time.Sleep(100 * time.Millisecond)
	}
}

func StartIpfsNode() (context.Context, icore.CoreAPI, context.CancelFunc, error) {
	fmt.Println("-- Getting an IPFS node running -- ")

	ctx, cancel := context.WithCancel(context.Background())

	// Spawn a node using a temporary path, creating a temporary repo for the run
	fmt.Println("Spawning Kubo node on a temporary repo")
	ipfsB, _, err := SpawnEphemeral(ctx)
	if err != nil {
		panic(fmt.Errorf("failed to spawn ephemeral node: %s", err))
	}

	fmt.Println("IPFS node is running")

	return ctx, ipfsB, cancel, err
}

var loadPluginsOnce sync.Once

// Spawns a node to be used just for this run (i.e. creates a tmp repo).
func SpawnEphemeral(ctx context.Context) (icore.CoreAPI, *core.IpfsNode, error) {
	var onceErr error
	loadPluginsOnce.Do(func() {
		onceErr = SetupPlugins("")
	})
	if onceErr != nil {
		return nil, nil, onceErr
	}

	// Create a Temporary Repo
	repoPath, err := CreateTempRepo()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create temp repo: %s", err)
	}

	node, err := CreateNode(ctx, repoPath)
	if err != nil {
		return nil, nil, err
	}

	api, err := coreapi.NewCoreAPI(node)

	return api, node, err
}

func UploadFiles(flagFilePath string) (cidStr string, err error) {
	ctx, ipfsA, cancel, err := StartIpfsNode()
	if err != nil {
		panic(fmt.Errorf("error: %s", err))
	}

	someFile, err := GetUnixfsNode(flagFilePath)
	if err != nil {
		panic(fmt.Errorf("error: %s", err))
	}

	//for the future simplicity to download single files in the same directory. Opened ticked on ipfs here: https://github.com/ipfs/boxo/issues/520
	fileInfo, err := os.Stat(flagFilePath)
	if err != nil {
		panic(fmt.Errorf("error: %s", err))
	}

	// wrap file into directory with filename so ipfs shows file name later as a workaround which doesn't allow to download into same directory
	if !fileInfo.IsDir() {
		someFile = files.NewSliceDirectory([]files.DirEntry{
			files.FileEntry(filepath.Base(flagFilePath), someFile),
		})
	}

	cidFile, err := ipfsA.Unixfs().Add(ctx, someFile)
	if err != nil {
		panic(fmt.Errorf("error: %s", err))
	}

	fmt.Printf("Added file to IPFS. Now share this CID with your friend:\n%s\n", cidFile.String())

	// you can find how many files and filenames with below counter code. Just try uploading/downloading single file from same dir and later upload directory
	c, err := ipfsA.Unixfs().Ls(ctx, cidFile)
	if err != nil {
		panic(fmt.Errorf("could not find Ls from Cid: %s", err))
	}
	fileCounter := 0
	for de := range c {
		fileCounter += 1
		fmt.Printf("%d file name: %v\n", fileCounter, de.Name)
	}

	fileSize, err := someFile.Size()
	if err != nil {
		panic(fmt.Errorf("error: %s", err))
	}

	fmt.Printf("Seeding size: %s\n", humanize.Bytes(uint64(fileSize)))

	go ForeverSpin()

	quitChannel := make(chan os.Signal, 1)
	signal.Notify(quitChannel, syscall.SIGINT, syscall.SIGTERM)
	<-quitChannel

	fmt.Println("\nAdios!")
	ctx.Done()
	defer cancel()

	return cidFile.String(), err
}

func GetCidStrFromString(str string) (cidStr string) {
	// in case of /ipfs/exampleCid we strip string and work only on exampleCid, in the future need to check if this is CID string
	cidStr = str[strings.LastIndex(str, "/")+1:]
	cidStr = strings.Trim(cidStr, " \r\n")
	return cidStr
}

func DownloadFromCid(cidStr string) (outputPath string, err error, progress int64) {

	ctx, ipfsA, cancel, err := StartIpfsNode()
	// ctx, ipfsA, cancel, err := StartIpfsNode()
	if err != nil {
		panic(fmt.Errorf("error: %s", err))
	}
	defer cancel()

	cidStr = GetCidStrFromString(cidStr)
	cidFromString, err := cid.Parse(cidStr)
	if err != nil {
		panic(fmt.Errorf("error: %s", err))
	}
	fmt.Printf("Fetching a file from the network with CID %s\n", cidStr)
	testCID := path.FromCid(cidFromString)

	rootNode, err := ipfsA.Unixfs().Get(ctx, testCID)
	if err != nil {
		panic(fmt.Errorf("error: %s", err))
	}

	ctx.Done()

	c, err := ipfsA.Unixfs().Ls(ctx, testCID)
	if err != nil {
		panic(fmt.Errorf("could not find Ls info from Cid: %s", err))
	}
	fileCounter := 0
	for de := range c {
		fileCounter += 1
		fmt.Printf("%d file name: %v\n", fileCounter, de.Name)
	}

	shouldWorkButNot := false // change to true and see how boxo doesn't let WriteTo same directory
	if shouldWorkButNot {
		outputPath = "." // save to same directory file
	} else {
		outputPath = "./Download/" + cidStr
	}

	err = os.MkdirAll("Download", 0o777)
	if err != nil {
		panic(fmt.Errorf("error: %s", err))
	}

	err = files.WriteTo(rootNode, filepath.Clean(outputPath))
	if err != nil {
		panic(fmt.Errorf("error: %s", err))
	} else {
		fmt.Printf("Wrote the files to %s\n", outputPath)
	}

	return outputPath, err, 100
}

func main() {

	var flagFilePath string
	flag.StringVar(&flagFilePath, "f", "", "a string path var") // filepath cli flag set

	var flagCid string
	flag.StringVar(&flagCid, "c", "", "a string cid var") // cid cli flag set

	flag.Parse()

	if flagCid != "" || flagFilePath != "" {
		if flagCid != "" {
			DownloadFromCid(flagCid)
		} else if flagFilePath != "" {
			UploadFiles(flagFilePath)
		}
	} else {
		fmt.Println("Use flags -f \"example.jpg\" or -c \"exampleCid\" to share files for example:\n./fsg -f \"example.jpg\"\nor to download files\n./fsg -c \"exampleCid\"")
	}
}
