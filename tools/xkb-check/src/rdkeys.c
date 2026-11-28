/* rdkeys: send key presses by evdev key code through mutter's remote-desktop D-Bus API.
 *
 * usage: rdkeys < events      (session bus of the running mutter or gnome-shell)
 *
 * mutter turns each code into a key of its own virtual keyboard, so the keys go
 * through the compositor's keymap, like a physical keyboard. The session lives as
 * long as this process keeps its bus connection, so one process sends every event.
 * events: "d CODE" / "u CODE" (XKB keycode = evdev code + 8), "s MS" sleeps.
 *
 * MIT-0.
 */
#include <gio/gio.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>

#define BUS "org.gnome.Mutter.RemoteDesktop"

static GVariant *call(GDBusConnection *c, const char *path, const char *iface, const char *method,
                      GVariant *args)
{
    GError *err = NULL;
    GVariant *r = g_dbus_connection_call_sync(c, BUS, path, iface, method, args, NULL,
                                              G_DBUS_CALL_FLAGS_NONE, 5000, NULL, &err);
    if (!r) {
        fprintf(stderr, "rdkeys: %s: %s\n", method, err->message);
        exit(1);
    }
    return r;
}

int main(void)
{
    GError *err = NULL;
    GDBusConnection *c = g_bus_get_sync(G_BUS_TYPE_SESSION, NULL, &err);
    if (!c) { fprintf(stderr, "rdkeys: %s\n", err->message); return 1; }
    GVariant *r = call(c, "/org/gnome/Mutter/RemoteDesktop", BUS, "CreateSession", NULL);
    const char *path;
    g_variant_get(r, "(&o)", &path);
    char *session = g_strdup(path);
    g_variant_unref(r);
    const char *si = BUS ".Session";
    g_variant_unref(call(c, session, si, "Start", NULL));
    /* A headless mutter has no keyboard until this session adds one, and the first key of
     * the new device was seen to get lost: tap Left Shift first. */
    usleep(300000);
    g_variant_unref(call(c, session, si, "NotifyKeyboardKeycode", g_variant_new("(ub)", 42, TRUE)));
    g_variant_unref(call(c, session, si, "NotifyKeyboardKeycode", g_variant_new("(ub)", 42, FALSE)));
    usleep(500000);
    char line[64];
    while (fgets(line, sizeof line, stdin)) {
        char kind;
        unsigned v;
        if (sscanf(line, "%c %u", &kind, &v) != 2) continue;
        if (kind == 's') { usleep(v * 1000); continue; }
        if ((kind != 'd' && kind != 'u') || v < 8) continue;
        g_variant_unref(call(c, session, si, "NotifyKeyboardKeycode",
                             g_variant_new("(ub)", v - 8, kind == 'd')));
        usleep(30000);
    }
    usleep(300000);
    g_variant_unref(call(c, session, si, "Stop", NULL));
    g_free(session);
    return 0;
}
