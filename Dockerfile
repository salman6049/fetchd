# Simple usage with a mounted data directory:
# > docker build -t gaia .
# > docker run -it -p 46657:46657 -p 46656:46656 -v ~/.wasmd:/root/.wasmd -v ~/.wasmcli:/root/.wasmcli gaia wasmd init
# > docker run -it -p 46657:46657 -p 46656:46656 -v ~/.wasmd:/root/.wasmd -v ~/.wasmcli:/root/.wasmcli gaia wasmd start
FROM golang:1.13-buster AS build-env

# Install minimum necessary dependencies, build Cosmos SDK, remove packages
RUN apt update
RUN apt install -y curl git build-essential

# Set working directory for the build
WORKDIR /go/src/github.com/cosmwasm/wasmd

# Add source files
COPY . .
#
RUN make tools
RUN make install

CMD ["wasmd"]
