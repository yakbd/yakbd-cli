# Distribution image: the UI-free typer and xkbcli on one distribution's own libxkbcommon.
ARG BASE
FROM ${BASE}
RUN if command -v apt-get >/dev/null; then \
      apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        gcc libc6-dev pkg-config libxkbcommon-dev libxkbcommon-tools xkb-data libx11-data locales \
      && (sed -i 's/^# *en_US.UTF-8/en_US.UTF-8/' /etc/locale.gen; locale-gen) \
      && rm -rf /var/lib/apt/lists/*; \
    elif command -v dnf >/dev/null; then \
      dnf -y install gcc pkgconf-pkg-config libxkbcommon-devel libxkbcommon-utils xkeyboard-config libX11-common glibc-langpack-en \
      && dnf clean all; \
    elif command -v pacman >/dev/null; then \
      pacman -Syu --noconfirm --needed gcc pkgconf libxkbcommon xkeyboard-config libx11 && pacman -Scc --noconfirm \
      && sed -i "s/^#en_US.UTF-8/en_US.UTF-8/" /etc/locale.gen && locale-gen; \
    fi
RUN (userdel -r ubuntu 2>/dev/null || true) && useradd -m -u 1000 tester
COPY src/ /opt/xkb-check/src/
RUN cd /opt/xkb-check/src && sh ./build.sh distro
