# Desktop image: every compositor, X11, GTK 4 and Qt 6 readback clients (Arch Linux, newest packages).
FROM archlinux:latest
RUN pacman -Syu --noconfirm --needed \
      gcc pkgconf libxkbcommon libxkbcommon-x11 xkeyboard-config \
      weston sway kwin gnome-shell mutter \
      gtk4 qt6-declarative qt6-wayland \
      xorg-server-xvfb xorg-xkbcomp xorg-setxkbmap xorg-xwayland libxtst libx11 \
      dbus glib2 mesa ttf-dejavu wayland plasma-wayland-protocols \
 && pacman -Scc --noconfirm
RUN sed -i "s/^#en_US.UTF-8/en_US.UTF-8/" /etc/locale.gen && locale-gen
# File capabilities (sway: cap_sys_nice) make exec fail in a rootless container: use plain copies.
RUN for b in sway kwin_wayland; do p=$(command -v $b); cp "$p" /usr/local/bin/$b; done
RUN (userdel -r ubuntu 2>/dev/null || true) && useradd -m -u 1000 tester
COPY src/ /opt/xkb-check/src/
RUN cd /opt/xkb-check/src && sh ./build.sh desktop
