/**
 * Base webpack config used across other specific configs
 */
import CircularDependencyPlugin from 'circular-dependency-plugin';
import * as dotenv from 'dotenv';
import ForkTsCheckerWebpackPlugin from 'fork-ts-checker-webpack-plugin';
import TsconfigPathsPlugins from 'tsconfig-paths-webpack-plugin';
import webpack from 'webpack';

import { dependencies as externals } from '../../release/app/package.json';

import webpackPaths from './webpack.paths';

dotenv.config();

const nestOptionalDependencies = [
  '@nestjs/microservices',
  '@nestjs/microservices/microservices-module',
  '@nestjs/websockets/socket-module',
  'class-transformer',
  'class-validator',
];

const configuration: webpack.Configuration = {
  externals: ['electron', ...Object.keys(externals || {})],

  stats: 'errors-only',

  watchOptions: {
    ignored: ['**/node_modules/**', '**/release/**', '**/.git/**'],
  },

  module: {
    rules: [
      {
        test: /\.[jt]sx?$/,
        exclude: /node_modules/,
        use: {
          loader: 'ts-loader',
          options: {
            // Remove this line to enable type checking in webpack builds
            transpileOnly: true,
            compilerOptions: {
              module: 'esnext',
            },
          },
        },
      },
    ],
  },

  output: {
    path: webpackPaths.srcPath,
    // https://github.com/webpack/webpack/issues/1114EnvironmentPlugin
    library: {
      type: 'commonjs2',
    },
  },

  /**
   * Determine the array of extensions that should be used to resolve modules.
   */
  resolve: {
    extensions: ['.js', '.jsx', '.json', '.ts', '.tsx'],
    modules: [webpackPaths.srcPath, 'node_modules'],
    // There is no need to add aliases here, the paths in tsconfig get mirrored
    plugins: [new TsconfigPathsPlugins()],
    fallback: {
      crypto: false,
      process: require.resolve('process/browser.js'),
      stream: require.resolve('stream-browserify'),
      vm: false,
    },
  },

  plugins: [
    new webpack.EnvironmentPlugin({
      NODE_ENV: 'production',
    }),
    new webpack.IgnorePlugin({
      checkResource(resource) {
        return nestOptionalDependencies.includes(resource);
      },
    }),
    new ForkTsCheckerWebpackPlugin(),
    new CircularDependencyPlugin({
      // exclude detection of files based on a RegExp
      exclude: /a\.js|node_modules/,
      // add errors to webpack instead of warnings
      failOnError: true,
      // allow import cycles that include an asyncronous import,
      // e.g. via import(/* webpackMode: "weak" */ './file.js')
      allowAsyncCycles: false,
      // set the current working directory for displaying module paths
      cwd: process.cwd(),
    }),
  ],
};

export default configuration;
