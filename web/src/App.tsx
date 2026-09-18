import React from 'react';
import { Routes, Route, Link } from 'react-router-dom';
import { DashboardView } from './views/DashboardView';
import { MonitorDetailView } from './views/MonitorDetailView';
import { NotFoundView } from './views/NotFoundView';
import { ShieldCheck, Terminal } from 'lucide-react';

export const App: React.FC = () => {
  return (
    <div className="min-h-screen bg-zinc-950 text-zinc-100 flex flex-col font-sans">
      {/* Top Navigation Bar */}
      <header className="sticky top-0 z-40 bg-zinc-950/80 backdrop-blur-md border-b border-zinc-800/80">
        <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 h-14 flex items-center justify-between">
          <Link
            to="/"
            className="flex items-center gap-2.5 group focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500 rounded-md py-1 px-1.5"
          >
            <div className="w-8 h-8 rounded-lg bg-blue-600/10 border border-blue-500/30 flex items-center justify-center text-blue-400 group-hover:border-blue-500/60 transition-colors">
              <ShieldCheck className="w-5 h-5 text-blue-400" />
            </div>
            <div className="flex flex-col">
              <span className="font-bold text-sm text-zinc-100 tracking-tight group-hover:text-blue-300 transition-colors">
                Deployment Watchdog
              </span>
              <span className="text-[10px] text-zinc-500 font-mono -mt-0.5">
                Runtime Observability
              </span>
            </div>
          </Link>

          <div className="flex items-center gap-3">
            <div className="hidden sm:flex items-center gap-1.5 px-2.5 py-1 rounded-md bg-zinc-900 border border-zinc-800 text-xs font-mono text-zinc-400">
              <span className="w-2 h-2 rounded-full bg-emerald-500 animate-pulse"></span>
              <span>Go Engine :8080</span>
            </div>
          </div>
        </div>
      </header>

      {/* Main Content Area */}
      <main className="flex-1 max-w-7xl w-full mx-auto px-4 sm:px-6 lg:px-8 py-6">
        <Routes>
          <Route path="/" element={<DashboardView />} />
          <Route path="/monitors/:id" element={<MonitorDetailView />} />
          <Route path="*" element={<NotFoundView />} />
        </Routes>
      </main>

      {/* Footer */}
      <footer className="border-t border-zinc-900 bg-zinc-950/40 text-xs text-zinc-500 py-4">
        <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 flex flex-col sm:flex-row items-center justify-between gap-2">
          <div className="flex items-center gap-1.5">
            <Terminal className="w-3.5 h-3.5 text-zinc-400" />
            <span>Deployment Watchdog Phase 6 Management Dashboard</span>
          </div>
          <div className="font-mono text-[11px] text-zinc-400">
            Postgres + Go Scheduler + React 19 SPA
          </div>
        </div>
      </footer>
    </div>
  );
};
export default App;
