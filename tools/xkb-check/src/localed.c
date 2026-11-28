/* localed: a stand-in for systemd-localed on a private system bus.
 *
 * usage: localed LAYOUT      (DBUS_SYSTEM_BUS_ADDRESS names the bus)
 *
 * Bare mutter (without gnome-shell) takes its keymap from the X11Layout, X11Variant,
 * X11Options and X11Model properties of org.freedesktop.locale1, as `localectl
 * set-x11-keymap` sets them on a real system. This program answers those properties
 * with LAYOUT (model pc105, no variant, no options) until it is killed.
 *
 * MIT-0.
 */
#include <gio/gio.h>
#include <stdio.h>
#include <stdlib.h>

static const char *layout;

static const char xml[] =
    "<node><interface name='org.freedesktop.locale1'>"
    "<property name='X11Layout' type='s' access='read'/>"
    "<property name='X11Model' type='s' access='read'/>"
    "<property name='X11Variant' type='s' access='read'/>"
    "<property name='X11Options' type='s' access='read'/>"
    "</interface></node>";

static GVariant *get_property(GDBusConnection *c, const char *sender, const char *path,
                              const char *iface, const char *name, GError **err, gpointer data)
{
    if (g_strcmp0(name, "X11Layout") == 0) return g_variant_new_string(layout);
    if (g_strcmp0(name, "X11Model") == 0) return g_variant_new_string("pc105");
    return g_variant_new_string("");
}

static const GDBusInterfaceVTable vtable = { NULL, get_property, NULL };

static void on_bus(GDBusConnection *c, const char *name, gpointer data)
{
    GDBusNodeInfo *info = g_dbus_node_info_new_for_xml(xml, NULL);
    g_dbus_connection_register_object(c, "/org/freedesktop/locale1", info->interfaces[0],
                                      &vtable, NULL, NULL, NULL);
}

static void on_name(GDBusConnection *c, const char *name, gpointer data)
{
    printf("localed ready\n");
    fflush(stdout);
}

static void on_lost(GDBusConnection *c, const char *name, gpointer data)
{
    fprintf(stderr, "localed: could not own %s\n", name);
    exit(1);
}

int main(int argc, char **argv)
{
    if (argc != 2) { fprintf(stderr, "usage: localed LAYOUT\n"); return 2; }
    layout = argv[1];
    g_bus_own_name(G_BUS_TYPE_SYSTEM, "org.freedesktop.locale1", G_BUS_NAME_OWNER_FLAGS_NONE,
                   on_bus, on_name, on_lost, NULL, NULL);
    g_main_loop_run(g_main_loop_new(NULL, FALSE));
    return 0;
}
