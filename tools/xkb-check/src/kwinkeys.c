/* kwinkeys: send key presses by evdev key code through KWin's fake-input protocol.
 *
 * usage: kwinkeys < events      (WAYLAND_DISPLAY names the running kwin_wayland)
 *
 * KWin turns each code into a key of a fake keyboard device inside its own input
 * stack, so the keys go through the compositor's keymap. The protocol code is generated
 * from the distribution's fake-input.xml when the image is built.
 * events: "d CODE" / "u CODE" (XKB keycode = evdev code + 8), "s MS" sleeps.
 *
 * MIT-0.
 */
#include <stdio.h>
#include <string.h>
#include <unistd.h>
#include <wayland-client.h>
#include "fake-input-client-protocol.h"

static struct org_kde_kwin_fake_input *fake;

static void global(void *data, struct wl_registry *reg, uint32_t name, const char *iface, uint32_t ver)
{
    if (strcmp(iface, org_kde_kwin_fake_input_interface.name) == 0 && ver >= 4)
        fake = wl_registry_bind(reg, name, &org_kde_kwin_fake_input_interface, 4);
}

static void global_remove(void *data, struct wl_registry *reg, uint32_t name) {}

static const struct wl_registry_listener listener = { global, global_remove };

int main(void)
{
    struct wl_display *d = wl_display_connect(NULL);
    if (!d) { fprintf(stderr, "kwinkeys: cannot connect\n"); return 1; }
    struct wl_registry *reg = wl_display_get_registry(d);
    wl_registry_add_listener(reg, &listener, NULL);
    wl_display_roundtrip(d);
    if (!fake) { fprintf(stderr, "kwinkeys: no org_kde_kwin_fake_input version 4\n"); return 1; }
    org_kde_kwin_fake_input_authenticate(fake, "xkb-check", "type test cases");
    wl_display_roundtrip(d);
    usleep(200000);
    char line[64];
    while (fgets(line, sizeof line, stdin)) {
        char kind;
        unsigned v;
        if (sscanf(line, "%c %u", &kind, &v) != 2) continue;
        if (kind == 's') { usleep(v * 1000); continue; }
        if ((kind != 'd' && kind != 'u') || v < 8) continue;
        org_kde_kwin_fake_input_keyboard_key(fake, v - 8, kind == 'd' ? 1 : 0);
        if (wl_display_roundtrip(d) < 0) { fprintf(stderr, "kwinkeys: connection lost\n"); return 1; }
        usleep(30000);
    }
    usleep(300000);
    wl_display_disconnect(d);
    return 0;
}
