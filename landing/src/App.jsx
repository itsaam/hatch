import { useState } from 'react'
import Layout from './components/Layout.jsx'
import Preloader from './components/Preloader.jsx'
import Hero from './sections/Hero.jsx'
import Problem from './sections/Problem.jsx'
import Solution from './sections/Solution.jsx'
import Flow from './sections/Flow.jsx'
import Why from './sections/Why.jsx'
import Status from './sections/Status.jsx'
import Cta from './sections/Cta.jsx'
import Footer from './components/Footer.jsx'

export default function App() {
  const [ready, setReady] = useState(false)
  return (
    <>
      <Preloader onDone={() => setReady(true)} />
      <Layout ready={ready}>
        <Hero ready={ready} />
        <Problem />
        <Solution />
        <Flow />
        <Why />
        <Status />
        <Cta />
        <Footer />
      </Layout>
    </>
  )
}
