# filesharegocli
File sharing P2P cli program made in GoLang with IPFS (Inter Planetary File System) via KISS (Keep It Super Simple) principle. OLD VERSION. NEW ONE CAN BE FOUND HERE: https://github.com/ofman/fsg
(this one is the simplest version and doesn't require filesharego library)
## Basic usage
Use flags -f "example.jpg" or -c "exampleCid" to share files for example:
Upload file (keep terminal window open/running to let others download):
   ```sh
   ./fsg -f example.jpg
   ```
Download file (open new terminal window):
   ```sh
   ./fsg -c /ipfs/QmX4zdEUtimXgxhpzv8jfFLqkuutNhmoNH987cH5RS67GM