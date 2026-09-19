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
      <header className="sticky top-0 z-40 bg-zinc-950/90 backdrop-blur-md border-b border-zinc-800/80">
        <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 h-14 flex items-center justify-between">
          <Link
            to="/"
            className="flex items-center gap-3 group focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500 rounded-lg py-1 px-1.5 -ml-1.5 transition-colors"
          >
            <div className="w-8 h-8 rounded-lg bg-blue-600/15 border border-blue-500/40 flex items-center justify-center text-blue-400 group-hover:border-blue-400/80 group-hover:bg-blue-600/20 transition-all shadow-xs">
              <ShieldCheck className="w-4.5 h-4.5 text-blue-400" />
            </div>
            <div className="flex flex-col">
              <span className="font-semibold text-sm text-zinc-100 tracking-tight group-hover:text-blue-300 transition-colors">
                Deployment Watchdog
              </span>
              <span className="text-[10px] text-zinc-400 font-mono tracking-wider uppercase -mt-0.5">
                Runtime Observability
              </span>
            </div>
          </Link>

          <div className="flex items-center gap-3">
            <div className="flex items-center gap-2 px-2.5 py-1 rounded-md bg-zinc-900/90 border border-zinc-800 text-xs font-mono text-zinc-300">
              <span className="relative flex h-2 w-2">
                <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75"></span>
                <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-500"></span>
              </span>
              <span className="text-[11px] text-zinc-400">Engine :8080</span>
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
      <footer className="border-t border-zinc-900 bg-zinc-950/60 text-xs text-zinc-500 py-4 mt-auto">
        <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 flex flex-col sm:flex-row items-center justify-between gap-2">
          <div className="flex items-center gap-1.5 text-zinc-400">
            <Terminal className="w-3.5 h-3.5 text-zinc-500" />
            <span>Deployment Watchdog — Continuous Endpoint Monitoring</span>
          </div>
          <div className="font-mono text-[11px] text-zinc-500">
            Go Engine + PostgreSQL + React 19
          </div>
        </div>
      </footer>
    </div>
  );
};
export default App;
