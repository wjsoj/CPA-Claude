import { StrictMode, Suspense, lazy } from "react";
import { createRoot } from "react-dom/client";
import { Toaster } from "@/components/ui/sonner";
import { ConfirmHost } from "@/hooks/use-confirm";
import { ThemeProvider } from "@/hooks/use-theme";
import "./styles/globals.css";

// Two unrelated apps live behind this one entry: the operator console and
// the public status page. Exactly one of them ever renders, decided by the
// path, so importing both statically made every visitor download the other
// one — including the whole recharts/d3 tree twice over. Splitting them here
// is what lets Rollup keep each app's code (and the chart library they only
// partly share) out of the initial download.
const App = lazy(() => import("./App").then((m) => ({ default: m.App })));
const StatusApp = lazy(() =>
  import("./StatusApp").then((m) => ({ default: m.StatusApp })),
);

const isStatus = window.location.pathname.startsWith("/status");

createRoot(document.getElementById("app")!).render(
  <StrictMode>
    <ThemeProvider>
      <Suspense fallback={<BootSplash />}>
        {isStatus ? <StatusApp /> : <App />}
      </Suspense>
      <ConfirmHost />
      <Toaster richColors closeButton position="top-right" />
    </ThemeProvider>
  </StrictMode>,
);

// Deliberately plain: this is on screen for the duration of one chunk fetch,
// and anything with layout of its own would cost a layout shift when the real
// app replaces it.
function BootSplash() {
  return (
    <div className="min-h-screen flex items-center justify-center">
      <div className="h-5 w-5 rounded-full border-2 border-primary/30 border-t-primary animate-spin" />
    </div>
  );
}
