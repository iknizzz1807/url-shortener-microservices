import * as React from 'react';
import { AuthScreen } from './components/magicpath/auth-screen/AuthScreen';
import { DashboardScreen } from './components/magicpath/dashboard-screen/DashboardScreen';

export default function App() {
  const [token, setToken] = React.useState(() => localStorage.getItem('linkstream_token'));
  const logout = () => {
    localStorage.removeItem('linkstream_token');
    setToken(null);
  };
  return token ? <DashboardScreen token={token} onLogout={logout} /> : <AuthScreen onAuthenticated={setToken} />;
}
