package epub

import (
	"archive/zip"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// SampleBookSpec defines a classic public-domain book to build as a valid EPUB 3 file.
type SampleBookSpec struct {
	UUID          string
	FileName      string
	Title         string
	Subtitle      string
	Authors       []string
	Series        string
	SeriesIndex   float64
	Tags          []string
	Description   string
	Publisher     string
	PublishedDate string
	Language      string
	ISBN          string
	Chapters      []struct {
		Title string
		Body  string
	}
}

// DefaultSampleSpecs returns 4 curated public-domain works with full multi-chapter text.
func DefaultSampleSpecs() []SampleBookSpec {
	return []SampleBookSpec{
		{
			UUID:          "a8c1f402-93b1-4b5e-812c-010000000001",
			FileName:      "The_Metamorphosis_-_Franz_Kafka.epub",
			Title:         "The Metamorphosis",
			Subtitle:      "Die Verwandlung",
			Authors:       []string{"Franz Kafka"},
			Series:        "Prague Classics",
			SeriesIndex:   1,
			Tags:          []string{"Existentialism", "Novella", "German Literature", "Classics"},
			Description:   "Traveling salesman Gregor Samsa wakes one morning to find himself transformed into a monstrous vermin. Kafka's landmark 1915 novella explores alienation, family duty, and the absurdity of modern existence with spare, unforgettable precision.",
			Publisher:     "Kurt Wolff Verlag / Folio Archival Editions",
			PublishedDate: "1915-10-01",
			Language:      "en",
			ISBN:          "9780486290300",
			Chapters: []struct {
				Title string
				Body  string
			}{
				{
					Title: "Part I: The Awakening",
					Body: `<p>One morning, when Gregor Samsa woke from troubled dreams, he found himself transformed in his bed into a horrible vermin. He lay on his armour-like back, and if he lifted his head a little he could see his brown belly, slightly domed and divided by arches into stiff sections. The bedding was hardly able to cover it and seemed ready to slide off any moment. His many legs, pitifully thin compared with the size of the rest of him, waved about helplessly as he looked.</p>
<p>“What’s happened to me?” he thought. It wasn’t a dream. His room, a proper human room although a little too small, lay peacefully between its four familiar walls. A collection of textile samples lay spread out on the table—Samsa was a travelling salesman—and above it there hung a picture that he had recently cut out of an illustrated magazine and housed in a nice, gilded frame. It showed a lady fitted out with a fur hat and fur boa who sat upright, raising a heavy fur muff that covered the whole of her lower arm towards the viewer.</p>
<p>Gregor then turned to look out the window at the dull weather. Drops of rain could be heard hitting the pane, which made him feel quite sad. “How about if I sleep a little bit longer and forget all this nonsense,” he thought, but that was something he was unable to do because he was used to sleeping on his right, and in his present state couldn’t get into that position. However hard he threw himself onto his right, he always rolled back to where he was. He must have tried it a hundred times, shut his eyes so that he wouldn’t have to look at the floundering legs, and only stopped when he began to feel a mild, dull pain there that he had never felt before.</p>
<p>“Oh, God,” he thought, “what a strenuous career it is that I’ve chosen! Travelling day in and day out. Doing business like this takes much more effort than doing your own business at home, and on top of that there’s the curse of travelling, worries about making train connections, bad and irregular food, contact with different people all the time so that you can never get to know anyone or become friendly with them.”</p>
<p>He looked over at the alarm clock, ticking on the chest of drawers. “God in Heaven!” he thought. It was half past six and the hands were quietly moving forwards, it was even later than half past, more like quarter to seven. Had the alarm clock not rung? He could see from the bed that it had been set for four o’clock as it should have been; it certainly must have rung. Yes, but was it possible to quietly sleep through that furniture-rattling noise? True, he had not slept peacefully, but probably all the more deeply because of that.</p>`,
				},
				{
					Title: "Part II: Behind the Locked Door",
					Body: `<p>Not until it was getting dark that evening did Gregor awake from a deep and coma-like sleep. Even if he hadn’t been woken up, he would certainly have woken not much later, as he felt himself sufficiently rested and wide awake, although it seemed to him as if a hurried step and a cautious shutting of the door leading into the hall had aroused him.</p>
<p>The gleam of the electric street lamps lay pale here and there on the ceiling and on the higher parts of the furniture, but down below, where Gregor was, it was dark. Pushing himself slowly towards the door, feeling his way clumsily with his antennae—which he was now learning to value—he wanted to check what had been happening there. His left side seemed one single long, unpleasantly stretched scar, and he really had to hobble on his two rows of legs.</p>
<p>By the door there was a dish filled with sweetened milk with little pieces of white bread floating in it. He could almost have laughed with joy, as he was now much hungrier than he had been in the morning, and immediately dipped his head evaluation-deep into the milk, nearly up to his eyes. But he soon drew it back again in disappointment; not only did he find it difficult to eat because of his sore left side—and he could only eat if his whole body worked together as a panting whole—but the milk did not taste at all nice, even though he had always liked it before, when his sister had brought it in for him.</p>
<p>During the long evenings, the family sat at the table under the lamplight, speaking in hushed tones about their modest savings and how the household might endure. Gregor listened attentively from the shadows of his threshold, wishing with all his strength that he could still step forward and shoulder their burden as he had done for so many tireless years.</p>`,
				},
				{
					Title: "Part III: The Violin and the Spring",
					Body: `<p>Gregor’s serious wound, from which he suffered for over a month—the apple remained embedded in his flesh as a visible souvenir since no one dared to remove it—seemed by itself to have reminded even his father that Gregor was a member of the family, despite his present pathetic and repulsive shape, who could not be treated as an enemy; that, on the contrary, family duty required the suppression of disgust and the exercise of patience, nothing but patience.</p>
<p>One evening, the sound of Grete’s violin drifted from the kitchen into the living room where the three lodgers were finishing their supper. Gregor crept slightly forward, drawn as if by an invisible thread toward the music. Was he an animal, that music could move him so? He felt as if the way to the unknown nourishment he craved was opening before him.</p>
<p>When spring finally arrived, the family took the tram out into the open country beyond the edge of the city. The car, in which they were the only passengers, was completely filled with warm sunshine. Leaning back comfortably in their seats, they talked over their prospects for the future, and on closer inspection these didn’t seem at all bad, as all three of them had employment that was exceptionally promising.</p>`,
				},
			},
		},
		{
			UUID:          "a8c1f402-93b1-4b5e-812c-010000000002",
			FileName:      "Flatland_-_Edwin_A_Abbott.epub",
			Title:         "Flatland",
			Subtitle:      "A Romance of Many Dimensions",
			Authors:       []string{"Edwin A. Abbott"},
			Series:        "Mathematical Fictions",
			SeriesIndex:   1,
			Tags:          []string{"Geometry", "Satire", "Science Fiction", "Victorian", "Mathematics"},
			Description:   "Narrated by A. Square, a mathematician resident in a two-dimensional world of geometric figures, Flatland is both a razor-sharp Victorian social satire and a mind-expanding exploration of higher spatial dimensions.",
			Publisher:     "Seeley & Co. / Folio Archival Editions",
			PublishedDate: "1884-11-01",
			Language:      "en",
			ISBN:          "9780486272634",
			Chapters: []struct {
				Title string
				Body  string
			}{
				{
					Title: "Section 1: Of the Nature of Flatland",
					Body: `<p>I call our world Flatland, not because we call it so, but to make its nature clearer to you, my happy readers, who are privileged to live in Space.</p>
<p>Imagine a vast sheet of paper on which straight Lines, Triangles, Squares, Pentagons, Hexagons, and other figures, instead of remaining fixed in their places, move freely about, on or in the surface, but without the power of rising above or sinking below it, very much like shadows—only hard and with luminous edges—and you will then have a pretty correct notion of my country and countrymen. Alas, a few years ago, I should have said “my universe”: but now my mind has been opened to higher views of things.</p>
<p>In such a country, you will perceive at once that it is impossible that there should be anything of what you call a “solid” kind; but I dare say you will suppose that we could at least distinguish by sight the Triangles, Squares, and other figures, moving about as I have described them. On the contrary, we could see nothing of the kind, not at least so as to distinguish one figure from another. Nothing was visible, nor could be visible, to us, except Straight Lines; and the necessity of this I will speedily demonstrate.</p>
<p>Place a penny on the middle of one of your tables in Space; and leaning over it, look down upon it. It will appear a circle. But now, drawing back to the edge of the table, gradually lower your eye (thus bringing yourself more and more into the condition of the inhabitants of Flatland), and you will find the penny becoming more and more oval to your view; and at last when you have placed your eye exactly on the edge of the table (so that you are, as it were, actually a Flatlander) the penny will then have ceased to appear oval at all, and will have become, so far as you can see, a straight line.</p>`,
				},
				{
					Title: "Section 2: Of the Climate and Houses in Flatland",
					Body: `<p>As with you, so also with us, there are four points of the compass, North, South, East, and West. There being no sun nor other heavenly bodies, it is impossible for us to determine the North in the usual way; but we have a method of our own. By a Law of Nature with us, there is a constant attraction to the South; and, although in temperate climates this is very slight—so that even a Woman in reasonable health can journey several furlongs northward without much difficulty—yet the hampering effect of the southward attraction is quite sufficient to serve as a compass in most parts of our earth.</p>
<p>The most common form for the construction of a house is five-sided or pentagonal. The two northern sides RO, OF, constitute the roof, and for the most part have no doors; on the East is a small door for the Women; on the West a much larger one for the Men; the South side or floor is usually doorless.</p>
<p>Square and triangular houses are not allowed, and for this reason. The angles of a Square (and still more those of an Equilateral Triangle) being much more pointed than those of a Pentagon, and the lines of inanimate objects (such as houses) being dimmer than the lines of Men and Women, it follows that there is no little danger lest the points of a square or triangular house residence might do serious injury to an inconsiderate or perhaps absent-minded traveller suddenly running against them: and therefore, as early as the eleventh century of our era, triangular houses were universally forbidden by Law.</p>`,
				},
				{
					Title: "Section 3: Concerning the Stranger from Spaceland",
					Body: `<p>It was the last day of the 1999th year of our era. The pattering of the rain had long ago ceased. Note by note, I was explaining to my little hexagonald grandson how the number of sides increases from generation to generation by the Law of Nature, and how a Square calculates his area by raising the length of a single side to the second power.</p>
<p>“And what,” asked the little Hexagon, “does the third power mean in Geometry?” “Nothing at all,” replied I, “not at least in Geometry; for Geometry has only Two Dimensions.” And then I proceeded to show the boy how a Point by moving through a length of three inches makes a Line of three inches, which may be represented by 3; and how a Line of three inches, moving parallel to itself through a length of three inches, makes a Square of three inches every way, which may be represented by three squared.</p>
<p>At that moment, a Voice from nowhere—neither North nor South, East nor West, but seemingly from my own interior—proclaimed: “The boy is a reasoning being! You are wrong: there is a Third Dimension, and I have come from Spaceland to reveal it to you.” Behold, a Circle appeared in the center of my room, growing wider and narrower without breaking its perimeter—a Sphere passing steadily through the plane of Flatland!</p>`,
				},
			},
		},
		{
			UUID:          "a8c1f402-93b1-4b5e-812c-010000000003",
			FileName:      "The_Art_of_War_-_Sun_Tzu.epub",
			Title:         "The Art of War",
			Subtitle:      "The Lionel Giles Translation",
			Authors:       []string{"Sun Tzu", "Lionel Giles"},
			Series:        "Eastern Philosophy & Strategy",
			SeriesIndex:   1,
			Tags:          []string{"Strategy", "Philosophy", "History", "Classics"},
			Description:   "Composed in the 5th century BC, Sun Tzu's treatise remains the definitive philosophical text on strategy, preparation, adaptability, and resolving conflict with minimal expenditure of force.",
			Publisher:     "Luzac & Co. / Folio Archival Editions",
			PublishedDate: "1910-03-15",
			Language:      "en",
			ISBN:          "9781590302255",
			Chapters: []struct {
				Title string
				Body  string
			}{
				{
					Title: "Chapter I: Laying Plans",
					Body: `<p>1. Sun Tzu said: The art of war is of vital importance to the State. It is a matter of life and death, a road either to safety or to ruin. Hence it is a subject of inquiry which can on no account be neglected.</p>
<p>2. The art of war, then, is governed by five constant factors, to be taken into account in one’s deliberations, when seeking to determine the conditions obtaining in the field. These are: (1) The Moral Law; (2) Heaven; (3) Earth; (4) The Commander; (5) Method and discipline.</p>
<p>3. The Moral Law causes the people to be in complete accord with their ruler, so that they will follow him regardless of their lives, undismayed by any danger. Heaven signifies night and day, cold and heat, times and seasons. Earth comprises distances, great and small; danger and security; open ground and narrow passes; the chances of life and death. The Commander stands for the virtues of wisdom, sincerity, benevolence, courage and strictness. By method and discipline are to be understood the marshaling of the army in its proper subdivisions, the graduations of rank among the officers, the maintenance of roads by which supplies may reach the army, and the control of military expenditure.</p>
<p>4. These five heads should be familiar to every general: he who knows them will be victorious; he who knows them not will fail.</p>`,
				},
				{
					Title: "Chapter II: Waging War & Supreme Excellence",
					Body: `<p>1. Sun Tzu said: In the practical art of war, the best thing of all is to take the enemy’s country whole and intact; to shatter and destroy it is not so good. So, too, it is better to recapture an army entire than to destroy it, to capture a regiment, a detachment or a company entire than to destroy them.</p>
<p>2. Hence to fight and conquer in all your battles is not supreme excellence; supreme excellence consists in breaking the enemy’s resistance without fighting.</p>
<p>3. Thus the highest form of generalship is to balk the enemy’s plans; the next best is to prevent the junction of the enemy’s forces; the next in order is to attack the enemy’s army in the field; and the worst policy of all is to besiege walled cities.</p>
<p>4. If you know the enemy and know yourself, you need not fear the result of a hundred battles. If you know yourself but not the enemy, for every victory gained you will also suffer a defeat. If you know neither the enemy nor yourself, you will succumb in every battle.</p>`,
				},
				{
					Title: "Chapter III: Energy and Weak Points",
					Body: `<p>1. There are not more than five musical notes, yet the combinations of these five give rise to more melodies than can ever be heard. There are not more than five primary colors (blue, yellow, red, white, and black), yet in combination they produce more hues than can ever be seen. There are not more than five cardinal tastes (sour, acrid, salt, sweet, bitter), yet combinations of them yield more flavors than can ever be tasted.</p>
<p>2. Water shapes its course according to the nature of the ground over which it flows; the soldier works out his victory in relation to the foe whom he is facing. Therefore, just as water retains no constant shape, so in warfare there are no constant conditions. He who can modify his tactics in relation to his opponent and thereby succeed in winning may be called a heaven-born captain.</p>`,
				},
			},
		},
		{
			UUID:          "a8c1f402-93b1-4b5e-812c-010000000004",
			FileName:      "Meditationes_de_Prima_Philosophia_-_Rene_Descartes.epub",
			Title:         "Meditations on First Philosophy",
			Subtitle:      "In Which the Existence of God and the Distinction of the Soul are Demonstrated",
			Authors:       []string{"René Descartes"},
			Series:        "Foundations of Modern Thought",
			SeriesIndex:   2,
			Tags:          []string{"Philosophy", "Epistemology", "Rationalism", "Classics"},
			Description:   "Descartes's 1641 philosophical masterpiece systematically doubts all sensory experience to establish an indubitable foundation for knowledge: Cogito, ergo sum.",
			Publisher:     "Michel Soly / Folio Archival Editions",
			PublishedDate: "1641-08-28",
			Language:      "en",
			ISBN:          "9780521558181",
			Chapters: []struct {
				Title string
				Body  string
			}{
				{
					Title: "Meditation I: Of the Things Which May Be Brought Within the Sphere of the Doubtful",
					Body: `<p>Several years have now elapsed since I first became aware that I had accepted, even from my youth, many false opinions for true, and that consequently what I afterward based on such principles was highly doubtful; and from that time I was convinced of the necessity of undertaking once in my life to rid myself of all the opinions I had adopted, and of commencing anew the work of building from the foundation, if I desired to establish a firm and abiding superstructure in the sciences.</p>
<p>For this purpose, then, it will not be necessary for me to show that the whole of my beliefs are false—a point, perhaps, which I shall never reach; but as even now my reason convinces me that I ought not the less carefully to withhold belief from what is not entirely certain and indubitable, than from what is manifestly false, it will be sufficient to justify the rejection of the whole if I shall find in each some ground for doubt.</p>
<p>All that I have, up to this moment, accepted as possessed of the highest truth and certainty, I received either from or through the senses. I observed, however, that these sometimes misled us; and it is the part of prudence not to place absolute confidence in that by which we have even once been deceived.</p>`,
				},
				{
					Title: "Meditation II: Of the Nature of the Human Mind",
					Body: `<p>The Meditation of yesterday has filled my mind with so many doubts, that it is no longer in my power to forget them. Nor do I see, meanwhile, any principle on which they can be resolved; and, just as if I had fallen all of a sudden into very deep water, I am so disconcerted that I can neither fix myfoot on the bottom nor swim to the surface.</p>
<p>Archimedes, that he might transport the entire globe from the place it occupied to another, demanded only a point that was firm and immovable; so, also, I shall be entitled to entertain the highest expectations, if I am fortunate enough to discover only one thing that is certain and indubitable.</p>
<p>But there is I know not what being, who is possessed at once of the highest power and the deepest cunning, who is constantly employing all his ingenuity in deceiving me. Doubtless, then, I exist, since I am deceived; and, let him deceive me as he may, he can never bring it about that I am nothing, so long as I shall be conscious that I am something. So that it must, in fine, be maintained, all things being maturely and carefully considered, that this proposition—<em>I am, I exist</em>—is necessarily true each time it is expressed by me, or conceived in my mind.</p>`,
				},
			},
		},
	}
}

// WriteSampleEPUB builds a complete, standards-compliant EPUB 3 archive at destPath.
func WriteSampleEPUB(destPath string, spec SampleBookSpec) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	f, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	defer zw.Close()

	// 1. mimetype (must be first and uncompressed)
	mimeWriter, err := zw.CreateHeader(&zip.FileHeader{
		Name:   "mimetype",
		Method: zip.Store,
	})
	if err != nil {
		return err
	}
	if _, err := io.WriteString(mimeWriter, "application/epub+zip"); err != nil {
		return err
	}

	// 2. META-INF/container.xml
	if err := writeZipText(zw, "META-INF/container.xml", `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`); err != nil {
		return err
	}

	// 3. OEBPS/cover.png
	coverPNG := GenerateTypographicCoverPNG(spec.Title, strings.Join(spec.Authors, ", "), spec.Series)
	if w, err := zw.Create("OEBPS/cover.png"); err == nil {
		_, _ = w.Write(coverPNG)
	}

	// 4. OEBPS/style.css
	if err := writeZipText(zw, "OEBPS/style.css", `body {
  font-family: Georgia, "Times New Roman", serif;
  line-height: 1.72;
  margin: 1.2em;
}
h1, h2 {
  font-weight: 600;
  line-height: 1.25;
  margin-top: 1.5em;
  margin-bottom: 0.75em;
}
p {
  margin: 0 0 1em 0;
  text-indent: 1.25em;
}
p:first-of-type {
  text-indent: 0;
}
`); err != nil {
		return err
	}

	// 5. Chapters XHTML
	var manifestItems strings.Builder
	var spineRefs strings.Builder
	var ncxNavPoints strings.Builder
	var navListItems strings.Builder

	for idx, ch := range spec.Chapters {
		chNum := idx + 1
		fileName := fmt.Sprintf("chapter-%d.xhtml", chNum)
		itemID := fmt.Sprintf("ch%d", chNum)

		xhtml := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="%s" lang="%s">
<head>
  <meta charset="utf-8"/>
  <title>%s</title>
  <link rel="stylesheet" type="text/css" href="style.css"/>
</head>
<body>
  <section id="sec-%d">
    <h2>%s</h2>
    %s
  </section>
</body>
</html>`, spec.Language, spec.Language, html.EscapeString(ch.Title), chNum, html.EscapeString(ch.Title), ch.Body)

		if err := writeZipText(zw, "OEBPS/"+fileName, xhtml); err != nil {
			return err
		}

		manifestItems.WriteString(fmt.Sprintf("    <item id=\"%s\" href=\"%s\" media-type=\"application/xhtml+xml\"/>\n", itemID, fileName))
		spineRefs.WriteString(fmt.Sprintf("    <itemref idref=\"%s\"/>\n", itemID))
		ncxNavPoints.WriteString(fmt.Sprintf(`    <navPoint id="navPoint-%d" playOrder="%d">
      <navLabel><text>%s</text></navLabel>
      <content src="%s"/>
    </navPoint>`+"\n", chNum, chNum, html.EscapeString(ch.Title), fileName))
		navListItems.WriteString(fmt.Sprintf("        <li><a href=\"%s\">%s</a></li>\n", fileName, html.EscapeString(ch.Title)))
	}

	// 6. OEBPS/nav.xhtml (EPUB 3 Navigation Document)
	navXHTML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<head><title>Table of Contents</title></head>
<body>
  <nav epub:type="toc" id="toc">
    <h1>Contents</h1>
    <ol>
%s    </ol>
  </nav>
</body>
</html>`, navListItems.String())
	if err := writeZipText(zw, "OEBPS/nav.xhtml", navXHTML); err != nil {
		return err
	}

	// 7. OEBPS/toc.ncx (EPUB 2 backward-compatible TOC)
	ncxXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
  <head>
    <meta name="dtb:uid" content="urn:uuid:%s"/>
  </head>
  <docTitle><text>%s</text></docTitle>
  <navMap>
%s  </navMap>
</ncx>`, html.EscapeString(spec.UUID), html.EscapeString(spec.Title), ncxNavPoints.String())
	if err := writeZipText(zw, "OEBPS/toc.ncx", ncxXML); err != nil {
		return err
	}

	// 8. OEBPS/content.opf
	var creatorsXML, subjectsXML strings.Builder
	for _, a := range spec.Authors {
		creatorsXML.WriteString(fmt.Sprintf("    <dc:creator>%s</dc:creator>\n", html.EscapeString(a)))
	}
	for _, t := range spec.Tags {
		subjectsXML.WriteString(fmt.Sprintf("    <dc:subject>%s</dc:subject>\n", html.EscapeString(t)))
	}
	subtitleXML := ""
	if spec.Subtitle != "" {
		subtitleXML = fmt.Sprintf("    <dc:title>%s</dc:title>\n", html.EscapeString(spec.Subtitle))
	}

	opfXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="BookId" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:identifier id="BookId">urn:uuid:%s</dc:identifier>
    <dc:identifier opf:scheme="ISBN">%s</dc:identifier>
    <dc:title>%s</dc:title>
%s%s%s    <dc:description>%s</dc:description>
    <dc:publisher>%s</dc:publisher>
    <dc:date>%s</dc:date>
    <dc:language>%s</dc:language>
    <meta name="calibre:series" content="%s"/>
    <meta name="calibre:series_index" content="%g"/>
    <meta name="cover" content="cover-img"/>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
    <item id="css" href="style.css" media-type="text/css"/>
    <item id="cover-img" href="cover.png" media-type="image/png" properties="cover-image"/>
%s  </manifest>
  <spine toc="ncx">
%s  </spine>
</package>`,
		html.EscapeString(spec.UUID),
		html.EscapeString(spec.ISBN),
		html.EscapeString(spec.Title),
		subtitleXML,
		creatorsXML.String(),
		subjectsXML.String(),
		html.EscapeString(spec.Description),
		html.EscapeString(spec.Publisher),
		html.EscapeString(spec.PublishedDate),
		html.EscapeString(spec.Language),
		html.EscapeString(spec.Series),
		spec.SeriesIndex,
		manifestItems.String(),
		spineRefs.String(),
	)

	return writeZipText(zw, "OEBPS/content.opf", opfXML)
}

func writeZipText(zw *zip.Writer, name, content string) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, content)
	return err
}
