package parser

import (
	"fmt"
	xmlreader "github.com/RishabhBhatnagar/gordf/rdfloader/xmlreader"
	"github.com/RishabhBhatnagar/gordf/uri"
	"sync"
)

const RDFNS = "http://www.w3.org/1999/02/22-rdf-syntax-ns#"

type Parser struct {
	Triples          map[string]*Triple
	writeLock        sync.RWMutex
	schemaDefinition map[string]uri.URIRef
	blankNodeGetter  BlankNodeGetter
	rdfNS            uri.URIRef
	wg               sync.WaitGroup
}

type Triple struct {
	Subject, Predicate, Object *Node
}

func (triple *Triple) Hash() string {
	return fmt.Sprintf("{%v; %v; %v}", triple.Subject, triple.Predicate, triple.Object)
}

func parseHeaderBlock(rootBlock xmlreader.Block) (map[string]uri.URIRef, error) {
	// returns all the schema definitions in the root block.
	// a schema definition is of the form xmlns:SchemaName="URI",

	namespaceURI := map[string]uri.URIRef{}

	for _, attr := range rootBlock.OpeningTag.Attrs {
		if attr.SchemaName == "xmlns" {
			uriref, err := uri.NewURIRef(attr.Value)
			if err != nil {
				return namespaceURI, fmt.Errorf("schema URI %v doesn't confirm to URL rules", rootBlock)
			}
			namespaceURI[attr.Name] = uriref
		}
	}
	return namespaceURI, nil
}

func (parser *Parser) uriFromPair(schemaName, name string) (mergedUri uri.URIRef, err error) {
	// returns the uri representation of a pair of strings.
	// name:schemaName is an example of pair.
	// pairs such as rdf:RDF, where, rdf must be a valid xmlns schema name.

	// base must be a valid schema name defined in the root tag.
	baseURI, ok := parser.schemaDefinition[schemaName]
	if !ok {
		return uri.URIRef{}, fmt.Errorf("undefined schema name: %v", schemaName)
	}

	// adding the relative fragment to the base uri.
	return baseURI.AddFragment(name), nil
}

func (parser *Parser) appendTriple(triple *Triple) {
	parser.writeLock.Lock()
	parser.Triples[triple.Hash()] = triple
	parser.writeLock.Unlock()
}

func (parser *Parser) getRDFAttributeIndex(tag xmlreader.Tag, attrName string) (index int, err error) {
	/*
		From all the attribute of the given tag, return the index of the attribute rdf:attrName
	*/
	index = -1
	for i, attr := range tag.Attrs {
		attrUri, err := parser.uriFromPair(attr.SchemaName, attr.Name)
		if err != nil {
			break
		}
		if attrUri == parser.rdfNS.AddFragment(attrName) {
			// current attribute is a rdf:attrName tag,
			index = i
			break
		}
	}
	return
}

func (parser *Parser) nodeFromTag(openingTag xmlreader.Tag) (node *Node, err error) {
	// returns the node object from the opening tag of any block.
	// https://www.w3.org/TR/rdf-syntax-grammar/figure1.png has sample image having 5 nodes.
	// 		one of them is a blank node.

	// description of the entire function:
	// if the opening tag has an attribute of rdf:about,
	//		the node will represented by the value of rdf:about attribute
	// else, it is a blank node.

	// checking if any of the attributes is a rdf:about attribute
	index, err := parser.getRDFAttributeIndex(openingTag, "about")
	if err != nil {
		return
	}

	var currentNode Node
	if index == -1 {
		// we didnt' find rdf:about in the attributes of the opening tag.
		// returning a new blank node.
		rdfNodeIDIndex, err := parser.getRDFAttributeIndex(openingTag, "nodeID")
		if err != nil {
			return nil, err
		}
		if rdfNodeIDIndex == -1 {
			currentNode = parser.blankNodeGetter.Get()
		} else {
			currentNode = parser.blankNodeGetter.GetFromId(openingTag.Attrs[rdfNodeIDIndex].Value)
		}
	} else {
		// we found a rdf:about tag.
		currentNode = Node{
			NodeType: IRI,
			ID:       openingTag.Attrs[index].Value,
		}
	}
	return &currentNode, nil
}

func New() (parser *Parser) {
	// creates a new parser object
	rdfNS, _ := uri.NewURIRef(RDFNS)
	return &Parser{
		Triples:          map[string]*Triple{},
		writeLock:        sync.RWMutex{},
		schemaDefinition: map[string]uri.URIRef{"": uri.URIRef{}},
		blankNodeGetter:  BlankNodeGetter{-1},
		wg:               sync.WaitGroup{},
		rdfNS:            rdfNS,
	}
}

func (parser *Parser) parseBlock(currBlock *xmlreader.Block, node *Node, errp *error) {
	/*
		1. What is a block?
		Ans: A rdf block is made up of
				1. Root Node (IRI Ref or BlankNode) :: Subject
				2. Link (IRI Ref)                   :: Object
				3. anotherBlock (Literal or IRI Ref or Blank Node) :: Predicate

		2. Example of a Block.
			Sample RDF/XML input with non-blank subject and literal predicate.
				<spdx:License rdf:about="http://spdx.org/licenses/Apache-2.0">
					<spdx:licenseId>Apache-2.0</spdx:licenseId>
				</spdx:License>
			Output Components:
				Subject:   http://spdx.org/licenses/Apache-2.0  (IRI Ref)
				Object:    spdx:licenseId						(IRI Ref)
				Predicate: Apacha-2.0							(Literal)

			If the rdf:about attribute of the subject is removed, it will become a blank node.

		3. What is a node *Node?
		Ans: effectively, node representation of the block parameter.
			 node := parser.nodeFromTag(block)

		4. Parameter errp.
			Pointer to an error variable.
			used to report errors in a concurrent environment.
			why pointer? Because go func() cannot return anything.
	*/
	for _, predicateBlock := range currBlock.Children {
		predicateNode, newErr := parser.nodeFromTag(predicateBlock.OpeningTag)
		*errp = newErr
		if *errp != nil {
			return
		}
		openingTagUri, newErr := parser.uriFromPair(currBlock.OpeningTag.SchemaName, currBlock.OpeningTag.Name)
		*errp = newErr
		if *errp != nil {
			return
		}
		predicateURI := parser.rdfNS.AddFragment("type")
		parser.appendTriple(&Triple{
			Subject:   node,
			Predicate: &Node{IRI, predicateURI.String()},
			Object:    &Node{IRI, openingTagUri.String()},
		})
		if *errp != nil {
			return
		}
		if len(predicateBlock.Children) == 0 {
			// no children.
			var objectString string
			resIdx, newErr := parser.getRDFAttributeIndex(predicateBlock.OpeningTag, "resource")
			*errp = newErr
			if *errp != nil {
				return
			}
			if resIdx != -1 {
				// rdf:resource attribute is present
				objectString = predicateBlock.OpeningTag.Attrs[resIdx].Value
			} else {
				objectString = predicateBlock.Value
			}

			// registering a new Triple:
			// (currentNode) -> rdf:type -> (openingTagURI)
			parser.appendTriple(&Triple{
				Subject:   node,
				Predicate: predicateNode,
				Object:    &Node{LITERAL, objectString},
			})
		}

		// the predicate block has children
		for _, objectBlock := range predicateBlock.Children {
			objectNode, newErr := parser.nodeFromTag(objectBlock.OpeningTag)
			*errp = newErr
			if *errp != nil {
				return
			}

			parser.appendTriple(&Triple{
				Subject:   node,
				Predicate: predicateNode,
				Object:    objectNode,
			})
			parser.wg.Add(1)
			go parser.parseBlock(objectBlock, objectNode, errp)
			if *errp != nil {
				return
			}
		}
	}
	parser.wg.Done()
}

func (parser *Parser) Parse(filePath string) (err error) {
	// reader for xml file
	reader, err := xmlreader.XMLReaderFromFilePath(filePath)
	if err != nil {
		return err
	}
	// parsing the xml file
	rootBlock, err := reader.Read()
	if err != nil {
		return err
	}

	// set all the schema definitions in the root block.
	schemaDefinition, err := parseHeaderBlock(rootBlock)
	if err != nil {
		return err
	}
	parser.schemaDefinition = schemaDefinition

	// root tag is set now.
	for _, child := range rootBlock.Children {
		parser.wg.Add(1)
		childNode, err := parser.nodeFromTag(child.OpeningTag)
		if err != nil {
			return err
		}
		go parser.parseBlock(child, childNode, &err)
		if err != nil {
			return err
		}
	}
	parser.wg.Wait()
	return nil
}
