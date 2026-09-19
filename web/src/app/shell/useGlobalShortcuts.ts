import { useEffect, useRef } from "react";
import { useNavigate } from "react-router-dom";
import { useTheme } from "../../shared/theme";

export interface GlobalShortcutsOptions {
  onOpenCommandPalette: () => void;
  isCommandPaletteOpen?: boolean;
}

export function isTypingInField(event: KeyboardEvent): boolean {
  const target = event.target as HTMLElement | null;
  if (!target) return false;

  const tagName = target.tagName ? target.tagName.toUpperCase() : "";
  if (tagName === "INPUT" || tagName === "TEXTAREA" || tagName === "SELECT") {
    return true;
  }
  if (
    target.isContentEditable ||
    target.getAttribute?.("contenteditable") === "true" ||
    target.getAttribute?.("contenteditable") === ""
  ) {
    return true;
  }
  if (target.getAttribute?.("role") === "textbox") {
    return true;
  }
  return false;
}

export function useGlobalShortcuts({
  onOpenCommandPalette,
  isCommandPaletteOpen = false,
}: GlobalShortcutsOptions): void {
  const navigate = useNavigate();
  const { toggleTheme } = useTheme();
  const chordRef = useRef<{ prefix: string; timer: NodeJS.Timeout | null }>({
    prefix: "",
    timer: null,
  });

  useEffect(() => {
    const clearChord = () => {
      if (chordRef.current.timer) {
        clearTimeout(chordRef.current.timer);
      }
      chordRef.current = { prefix: "", timer: null };
    };

    const handleKeyDown = (e: KeyboardEvent) => {
      // 1. Mod+K (Ctrl+K or Cmd+K) always opens/focuses the command palette
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        e.stopPropagation();
        onOpenCommandPalette();
        clearChord();
        return;
      }

      // If command palette is open, palette handles its own internal navigation
      if (isCommandPaletteOpen) {
        return;
      }

      // 2. Safe guards: never trigger single-key or chord shortcuts while operator types in an input/textarea/select
      if (isTypingInField(e)) {
        clearChord();
        return;
      }

      // 3. Modifiers check: avoid conflicting with browser combinations (Alt, Ctrl, Meta)
      if (e.ctrlKey || e.metaKey || e.altKey) {
        clearChord();
        return;
      }

      const key = e.key;

      // 4. Help shortcut '?' opens command palette
      if (key === "?") {
        e.preventDefault();
        onOpenCommandPalette();
        clearChord();
        return;
      }

      // 5. Chord sequence: 'g' prefix for "go to..."
      if (chordRef.current.prefix === "g") {
        clearChord();
        switch (key.toLowerCase()) {
          case "o":
            e.preventDefault();
            navigate("/overview");
            return;
          case "u":
            e.preventDefault();
            navigate("/usage");
            return;
          case "k":
            e.preventDefault();
            navigate("/keys");
            return;
          case "r":
            e.preventDefault();
            navigate("/requests");
            return;
          case "s":
            e.preventDefault();
            navigate("/system");
            return;
          default:
            return;
        }
      }

      // Initial chord trigger
      if (key.toLowerCase() === "g") {
        chordRef.current.prefix = "g";
        chordRef.current.timer = setTimeout(() => {
          clearChord();
        }, 1200);
        return;
      }

      // 6. Direct single-key shortcuts when not typing:
      // 't' toggles theme
      if (key.toLowerCase() === "t") {
        e.preventDefault();
        toggleTheme();
        clearChord();
        return;
      }

      clearChord();
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => {
      window.removeEventListener("keydown", handleKeyDown);
      clearChord();
    };
  }, [navigate, toggleTheme, onOpenCommandPalette, isCommandPaletteOpen]);
}
