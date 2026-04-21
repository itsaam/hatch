import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App.jsx'
import AdminPage from './admin/AdminPage.jsx'
import './styles/tokens.css'
import './styles/global.css'

const isAdmin = typeof window !== 'undefined' && window.location.pathname.startsWith('/admin')

ReactDOM.createRoot(document.getElementById('root')).render(
  isAdmin ? <AdminPage /> : <App />,
)
