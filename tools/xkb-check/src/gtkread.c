/* gtkread: a GTK 4 window with one text view; writes what was typed into it.
 *
 * usage: gtkread OUTFILE
 *
 * Writes OUTFILE.ready once the window is active (has the keyboard focus) and rewrites
 * OUTFILE with the whole text after every change. Dead keys and Compose go through
 * GTK's own input method handling, exactly as in any GTK 4 application.
 *
 * MIT-0.
 */
#include <gtk/gtk.h>
#include <glib/gstdio.h>

static const char *out;

static void write_file(const char *path, const char *text)
{
    char *tmp = g_strconcat(path, ".tmp", NULL);
    if (g_file_set_contents(tmp, text, -1, NULL))
        g_rename(tmp, path);
    g_free(tmp);
}

static void on_changed(GtkTextBuffer *buf, gpointer data)
{
    GtkTextIter a, b;
    gtk_text_buffer_get_bounds(buf, &a, &b);
    char *text = gtk_text_buffer_get_text(buf, &a, &b, FALSE);
    write_file(out, text);
    g_free(text);
}

static void on_active(GObject *win, GParamSpec *ps, gpointer data)
{
    if (gtk_window_is_active(GTK_WINDOW(win))) {
        char *ready = g_strconcat(out, ".ready", NULL);
        write_file(ready, "ready\n");
        g_free(ready);
    }
}

static void activate(GtkApplication *app, gpointer data)
{
    GtkWidget *win = gtk_application_window_new(app);
    gtk_window_set_default_size(GTK_WINDOW(win), 600, 400);
    GtkWidget *view = gtk_text_view_new();
    gtk_window_set_child(GTK_WINDOW(win), view);
    g_signal_connect(gtk_text_view_get_buffer(GTK_TEXT_VIEW(view)), "changed", G_CALLBACK(on_changed), NULL);
    g_signal_connect(win, "notify::is-active", G_CALLBACK(on_active), NULL);
    gtk_window_present(GTK_WINDOW(win));
    gtk_widget_grab_focus(view);
}

int main(int argc, char **argv)
{
    if (argc != 2) { g_printerr("usage: gtkread OUTFILE\n"); return 2; }
    out = argv[1];
    GtkApplication *app = gtk_application_new(NULL, G_APPLICATION_NON_UNIQUE);
    g_signal_connect(app, "activate", G_CALLBACK(activate), NULL);
    char *args[] = { argv[0], NULL };
    int rc = g_application_run(G_APPLICATION(app), 1, args);
    g_object_unref(app);
    return rc;
}
