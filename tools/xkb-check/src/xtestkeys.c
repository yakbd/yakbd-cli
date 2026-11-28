/* xtestkeys: send key presses by key code to an X server with XTest.
 *
 * usage: xtestkeys < events      (DISPLAY names the server)
 *        xtestkeys focus         (only the focus step)
 *        xtestkeys names LAYOUT  (only set the root property _XKB_RULES_NAMES to evdev,pc105,LAYOUT:
 *                                 what a nested weston compiles its keymap from)
 *
 * First it gives the input focus to the largest mapped window and moves the pointer
 * into it, so a nested compositor's window or a client gets the keys without a window
 * manager. events: "d CODE" / "u CODE" (X keycode = evdev code + 8), "s MS" sleeps.
 *
 * MIT-0.
 */
#include <stdio.h>
#include <unistd.h>
#include <X11/Xlib.h>
#include <X11/Xatom.h>
#include <X11/extensions/XTest.h>

static void focus_top(Display *d)
{
    Window root = DefaultRootWindow(d), r, p, *kids = NULL;
    unsigned n = 0;
    if (!XQueryTree(d, root, &r, &p, &kids, &n)) return;
    /* The largest viewable window: toolkits also map small helper windows. */
    Window best = 0;
    XWindowAttributes ba;
    long best_area = 0;
    for (unsigned i = 0; i < n; i++) {
        XWindowAttributes a;
        if (XGetWindowAttributes(d, kids[i], &a) && a.map_state == IsViewable && !a.override_redirect &&
            (long)a.width * a.height > best_area) {
            best = kids[i];
            ba = a;
            best_area = (long)a.width * a.height;
        }
    }
    if (best) {
        XSetInputFocus(d, best, RevertToPointerRoot, CurrentTime);
        XTestFakeMotionEvent(d, -1, ba.x + ba.width / 2, ba.y + ba.height / 2, 0);
    }
    if (kids) XFree(kids);
    XSync(d, False);
}

int main(int argc, char **argv)
{
    Display *d = XOpenDisplay(NULL);
    if (!d) { fprintf(stderr, "xtestkeys: cannot open display\n"); return 1; }
    int ev, er, maj, min;
    if (!XTestQueryExtension(d, &ev, &er, &maj, &min)) { fprintf(stderr, "xtestkeys: no XTest\n"); return 1; }
    if (argc > 2) {
        static char v[256];
        int n = snprintf(v, sizeof v, "evdev%cpc105%c%s%c%c", 0, 0, argv[2], 0, 0);
        Atom a = XInternAtom(d, "_XKB_RULES_NAMES", False);
        XChangeProperty(d, DefaultRootWindow(d), a, XA_STRING, 8, PropModeReplace, (unsigned char *)v, n);
        XCloseDisplay(d);
        return 0;
    }
    focus_top(d);
    if (argc > 1) { XCloseDisplay(d); return 0; }
    usleep(200000);
    char line[64];
    while (fgets(line, sizeof line, stdin)) {
        char kind;
        unsigned v;
        if (sscanf(line, "%c %u", &kind, &v) != 2) continue;
        if (kind == 's') { XSync(d, False); usleep(v * 1000); continue; }
        if (kind != 'd' && kind != 'u') continue;
        XTestFakeKeyEvent(d, v, kind == 'd', CurrentTime);
        XSync(d, False);
        usleep(30000);
    }
    XCloseDisplay(d);
    return 0;
}
