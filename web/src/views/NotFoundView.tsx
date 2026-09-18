import React from 'react';
import { Link } from 'react-router-dom';
import { Button } from '../components/common/Button';
import { Home, Compass } from 'lucide-react';

export const NotFoundView: React.FC = () => {
  return (
    <div className="py-20 text-center">
      <div className="w-16 h-16 rounded-full bg-zinc-900 border border-zinc-800 flex items-center justify-center mx-auto mb-4 text-zinc-500">
        <Compass className="w-8 h-8" />
      </div>
      <h1 className="text-3xl font-bold font-mono text-zinc-100 mb-2">404</h1>
      <h2 className="text-lg font-semibold text-zinc-300 mb-2">Page Not Found</h2>
      <p className="text-sm text-zinc-500 max-w-sm mx-auto mb-6">
        The page you requested does not exist or the monitor was removed.
      </p>
      <Link to="/">
        <Button variant="primary" icon={<Home className="w-4 h-4" />}>
          Back to Dashboard
        </Button>
      </Link>
    </div>
  );
};
