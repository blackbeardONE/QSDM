import React from 'react';

type State = {
  error?: Error;
};

type Props = {
  children: React.ReactNode;
};

export class AppErrorBoundary extends React.Component<Props, State> {
  state: State = {};

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: React.ErrorInfo) {
    console.error('Renderer crash captured by AppErrorBoundary', {
      error,
      componentStack: info.componentStack,
    });
  }

  render() {
    const { error } = this.state;
    const { children } = this.props;

    if (!error) return children;

    return (
      <div className="flex h-screen w-screen items-center justify-center bg-main-gradient p-8 text-white">
        <div className="max-w-xl rounded bg-finnieBlue-light-secondary p-8 text-center shadow-lg">
          <h1 className="mb-4 text-2xl font-semibold">QSDM Hive needs a refresh</h1>
          <p className="mb-6 text-sm leading-6">
            The window hit a renderer error, but the app is still running. Close
            and reopen QSDM Hive, or use the desktop window reload shortcut.
          </p>
          <pre className="max-h-40 overflow-auto rounded bg-black/30 p-3 text-left text-xs">
            {error.message}
          </pre>
        </div>
      </div>
    );
  }
}
