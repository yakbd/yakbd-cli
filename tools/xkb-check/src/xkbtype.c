/* xkbtype: type key events through libxkbcommon's state and Compose API, with no display.
 *
 * usage: xkbtype LAYOUT < events      (LAYOUT may be NAME(VARIANT))
 *
 * The keymap is compiled from the layout name alone (rules evdev, model pc105), so the
 * layout is found the way a compositor finds it: through libxkbcommon's default include
 * path, which puts $XDG_CONFIG_HOME/xkb (or ~/.config/xkb) first. The Compose table is
 * the one libxkbcommon picks for the locale ($XCOMPOSEFILE, $XDG_CONFIG_HOME/XCompose,
 * ~/.XCompose, then the system table).
 *
 * events: one per line, "d CODE" (key down) or "u CODE" (key up), CODE an XKB keycode
 * (evdev code + 8). Other lines are ignored. The typed text goes to stdout; Return
 * gives "\n".
 *
 * MIT-0.
 */
#include <locale.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <xkbcommon/xkbcommon.h>
#include <xkbcommon/xkbcommon-compose.h>

static const char *locale_name(void)
{
    const char *l = getenv("LC_ALL");
    if (!l || !*l) l = getenv("LC_CTYPE");
    if (!l || !*l) l = getenv("LANG");
    if (!l || !*l) l = "C";
    return l;
}

int main(int argc, char **argv)
{
    if (argc != 2) {
        fprintf(stderr, "usage: xkbtype LAYOUT < events\n");
        return 2;
    }
    setlocale(LC_ALL, "");
    struct xkb_context *ctx = xkb_context_new(XKB_CONTEXT_NO_FLAGS);
    if (!ctx) { fprintf(stderr, "xkbtype: no context\n"); return 1; }
    /* "NAME(VARIANT)", as the generated cases of a variant name it: the variant goes in its own field. */
    char layout[256], variant[256] = "";
    snprintf(layout, sizeof layout, "%s", argv[1]);
    char *paren = strchr(layout, '(');
    if (paren && layout[strlen(layout) - 1] == ')') {
        layout[strlen(layout) - 1] = 0;
        snprintf(variant, sizeof variant, "%s", paren + 1);
        *paren = 0;
    }
    struct xkb_rule_names names = { "evdev", "pc105", layout, variant, "" };
    struct xkb_keymap *km = xkb_keymap_new_from_names(ctx, &names, XKB_KEYMAP_COMPILE_NO_FLAGS);
    if (!km) { fprintf(stderr, "xkbtype: keymap for layout %s did not compile\n", argv[1]); return 1; }
    struct xkb_state *st = xkb_state_new(km);
    struct xkb_compose_table *ct = xkb_compose_table_new_from_locale(ctx, locale_name(),
                                                                     XKB_COMPOSE_COMPILE_NO_FLAGS);
    struct xkb_compose_state *cs = ct ? xkb_compose_state_new(ct, XKB_COMPOSE_STATE_NO_FLAGS) : NULL;
    if (!cs) fprintf(stderr, "xkbtype: no Compose table for %s\n", locale_name());

    char line[64];
    while (fgets(line, sizeof line, stdin)) {
        char kind;
        unsigned code;
        if (sscanf(line, "%c %u", &kind, &code) != 2 || (kind != 'd' && kind != 'u'))
            continue;
        if (kind == 'u') {
            xkb_state_update_key(st, code, XKB_KEY_UP);
            continue;
        }
        const xkb_keysym_t *syms;
        int n = xkb_state_key_get_syms(st, code, &syms);
        char buf[256] = "";
        if (n == 1 && syms[0] == XKB_KEY_Return) {
            if (cs) xkb_compose_state_reset(cs);
            strcpy(buf, "\n");
        } else if (n == 1 && cs) {
            xkb_compose_state_feed(cs, syms[0]);
            switch (xkb_compose_state_get_status(cs)) {
            case XKB_COMPOSE_COMPOSING:
            case XKB_COMPOSE_CANCELLED:
                break;
            case XKB_COMPOSE_COMPOSED:
                xkb_compose_state_get_utf8(cs, buf, sizeof buf);
                xkb_compose_state_reset(cs);
                break;
            case XKB_COMPOSE_NOTHING:
                xkb_state_key_get_utf8(st, code, buf, sizeof buf);
                break;
            }
        } else if (n > 0) {
            /* Several keysyms on one level: no Compose, the whole string. */
            xkb_state_key_get_utf8(st, code, buf, sizeof buf);
        }
        /* Control characters other than the newline are not text. */
        if (buf[0] && !(buf[1] == 0 && (unsigned char)buf[0] < 0x20 && buf[0] != '\n'))
            fputs(buf, stdout);
        xkb_state_update_key(st, code, XKB_KEY_DOWN);
    }
    fflush(stdout);
    return 0;
}
