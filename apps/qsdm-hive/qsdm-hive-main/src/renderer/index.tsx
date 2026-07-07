import React from 'react';
import * as ReactDOM from 'react-dom/client';

import { AppErrorBoundary } from 'renderer/components/AppErrorBoundary/AppErrorBoundary';

import App from './App';

window.addEventListener('error', (event) => {
  console.error('Renderer window error', event.error || event.message);
});

window.addEventListener('unhandledrejection', (event) => {
  console.error('Renderer unhandled rejection', event.reason);
});

const container = document.getElementById('root')!;
const root = ReactDOM.createRoot(container);

root.render(
  <React.StrictMode>
    <AppErrorBoundary>
      <App />
    </AppErrorBoundary>
  </React.StrictMode>
);
