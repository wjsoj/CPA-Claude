import { Moon, Sun } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useTheme } from "@/hooks/use-theme";
import { cn } from "@/lib/utils";

interface Props {
  className?: string;
}

export function ThemeToggle({ className }: Props) {
  const { theme, toggle } = useTheme();
  const isDark = theme === "dark";
  return (
    <Button
      type="button"
      variant="outline"
      size="icon"
      onClick={toggle}
      aria-label={isDark ? "Switch to light" : "Switch to dark"}
      title={isDark ? "Light" : "Dark"}
      className={cn(
        "relative overflow-hidden border-border-strong bg-card/60 hover:bg-card transition-all",
        className,
      )}
    >
      <Sun
        className={cn(
          "h-4 w-4 absolute transition-all duration-500",
          isDark ? "opacity-0 rotate-90 scale-50" : "opacity-100 rotate-0 scale-100",
        )}
      />
      <Moon
        className={cn(
          "h-4 w-4 absolute transition-all duration-500",
          isDark ? "opacity-100 rotate-0 scale-100" : "opacity-0 -rotate-90 scale-50",
        )}
      />
    </Button>
  );
}
