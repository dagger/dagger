FROM gitpod/workspace-go

RUN sudo mkdir -p /workspace/go/.cache && sudo chown -R gitpod:gitpod /workspace \
    && go install -v github.com/mikefarah/yq/v4@latest \
    && go install -v honnef.co/go/tools/cmd/staticcheck@latest \
    && sudo apt-add-repository ppa:fish-shell/release-3 \
    && sudo apt-add-repository ppa:neovim-ppa/unstable \
    && sudo add-apt-repository ppa:lazygit-team/release \
    && sudo apt update && sudo install-packages \
        fish \
        fzf \
        lazygit \
        luajit \
        neovim \
        shellcheck \
        tmux \
        tree