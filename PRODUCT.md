# Hatch

> Chaque PR éclot en preview live, instantanément.

---

## Le problème

Quand un développeur ouvre une Pull Request, **personne ne peut tester sa feature sans galérer**.

Pour la voir, il faut :
- Cloner le projet
- Installer toutes les dépendances
- Démarrer la base de données
- Lancer le serveur en local
- Espérer que tout marche du premier coup

Résultat : le designer ne teste pas. Le PM valide à l'aveugle. Le mentor lit le code mais ne voit pas le rendu réel. Le reviewer perd 15 minutes à chaque PR juste pour ouvrir l'app. Les bugs visuels passent à travers, les retours arrivent trop tard, les itérations s'enlisent.

Vercel résout ça pour les sites Next.js. Mais si tu utilises **autre chose** — du .NET, du Django, une app Docker custom, un backend Go — tu retombes dans la galère.

---

## La solution

**Hatch déploie automatiquement chaque PR en environnement live, sur ta propre infrastructure.**

Le développeur push sa branche. Hatch éclot une preview. Une URL atterrit dans le commentaire de la PR. Tout le monde clique. Tout le monde teste, dans son navigateur, sans rien installer.

Quand la PR est mergée ou fermée, Hatch nettoie tout, automatiquement.

---

## Pour qui

- **Petites équipes** qui veulent du Vercel-like sans payer Vercel et sans dépendre d'un cloud externe
- **Freelances** qui livrent des PR à leurs clients et veulent qu'ils valident visuellement avant merge
- **Devs solo en alternance ou en école** qui veulent montrer leur code à leur tuteur sans qu'il ait à tout installer
- **Projets open-source** qui acceptent des contributions externes : le mainteneur teste sans cloner

---

## La promesse

1. **Aucune dépendance à un cloud propriétaire.** Hatch tourne sur ton serveur, tes règles.
2. **Aucune installation côté reviewer.** Un clic sur le lien dans la PR, c'est tout.
3. **Aucun ménage manuel.** Une PR fermée = un environnement détruit. Pas de containers oubliés.
4. **Aucune attente.** Une preview prête en moins d'une minute après le push.
5. **Tout est isolé.** Chaque PR a sa propre URL, sa propre base de données, ses propres variables.

---

## Ce que ça change concrètement

**Avant Hatch**
> "J'ai fait la wishlist, regarde la PR." → 3 jours de back-and-forth, le designer abandonne, le merge se fait sans validation visuelle, on découvre le bug en prod.

**Avec Hatch**
> "J'ai fait la wishlist, voilà le lien." → 5 minutes plus tard tout le monde a testé, deux retours en commentaires, merge propre dans la journée.

---

## Le nom

**Hatch** — éclore, faire sortir d'un coup. Une PR éclot en preview, vit le temps de la review, disparaît à la fermeture.
