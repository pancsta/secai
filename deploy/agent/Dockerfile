FROM archlinux:latest

# OS

RUN pacman -Sy --noconfirm go git ca-certificates \
     ttyd go-task zellij which openssh wget
RUN mkdir -p /app/secai
RUN cp /usr/bin/go-task /usr/bin/task
RUN mkdir -p ~/.config/zellij/
RUN echo "show_startup_tips false" > ~/.config/zellij/config.kdl
RUN echo "show_release_notes false" >> ~/.config/zellij/config.kdl
RUN wget -O /usr/bin/fblog https://github.com/brocode/fblog/releases/download/v4.14.0/fblog
RUN chmod +x /usr/bin/fblog

# DEPS

COPY ./asyncmachine-go /app/asyncmachine-go
COPY ./cview /app/cview
COPY ./secai/go.mod /app/secai
COPY ./secai/go.sum /app/secai
COPY ./secai/Taskfile.yml /app/secai
COPY ./secai/config /app/secai
WORKDIR /app/secai
RUN --mount=type=cache,target=/root/go/pkg/mod \
   task install-deps
RUN cp ~/go/bin/goreman /usr/bin/
RUN cp ~/go/bin/static-server /usr/bin/

# PREBUILD

RUN --mount=type=cache,target=/root/go/pkg/mod \
    go mod download

# SOURCE

COPY ./secai /app/secai
COPY ./secai/ttyd.html /app/secai/ttyd.html
COPY ./secai/ttyd-mobile.html /app/secai/ttyd-mobile.html
RUN --mount=type=cache,target=/root/.cache/go-build \
    task build

# START

EXPOSE 7000-8000
ENTRYPOINT ["task", "start"]
